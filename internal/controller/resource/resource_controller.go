/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/mandelsoft/goutils/sliceutils"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"ocm.software/ocm/api/datacontext"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/git"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/github"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/helm"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
	"ocm.software/ocm/api/ocm/resolvers"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	giturls "github.com/chainguard-dev/git-urls"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	ocmctx "ocm.software/ocm/api/ocm"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/util"
)

type Reconciler struct {
	*ocm.BaseReconciler
}

var _ ocm.Reconciler = (*Reconciler)(nil)

var deployerIndex = "Resource.spec.resourceRef"

func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Create index for component reference name from resources
	const fieldName = "spec.componentRef.name"
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.Resource{}, fieldName, func(obj client.Object) []string {
		resource, ok := obj.(*v1alpha1.Resource)
		if !ok {
			return nil
		}

		return []string{resource.Spec.ComponentRef.Name}
	}); err != nil {
		return err
	}

	// This index is required to get all deployers that reference a resource. This is required to make sure that when
	// deleting the resource, no deployer exists anymore that references that resource.
	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&v1alpha1.Deployer{},
		deployerIndex,
		func(obj client.Object) []string {
			deployer, ok := obj.(*v1alpha1.Deployer)
			if !ok {
				return nil
			}

			return []string{fmt.Sprintf(
				"%s/%s",
				deployer.Spec.ResourceRef.Namespace,
				deployer.Spec.ResourceRef.Name,
			)}
		},
	); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Resource{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// Watch for component-events that are referenced by resources
		Watches(
			&v1alpha1.Component{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				component, ok := obj.(*v1alpha1.Component)
				if !ok {
					return []reconcile.Request{}
				}

				// Get list of resources that reference the component
				list := &v1alpha1.ResourceList{}
				if err := r.List(ctx, list, client.MatchingFields{fieldName: component.GetName()}); err != nil {
					return []reconcile.Request{}
				}

				// For every resource that references the component create a reconciliation request for that resource
				requests := make([]reconcile.Request, 0, len(list.Items))
				for _, resource := range list.Items {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Namespace: resource.GetNamespace(),
							Name:      resource.GetName(),
						},
					})
				}

				return requests
			})).
		Watches(
			// Ensure to reconcile the resource when a deployer changes that references this resource. We want to
			// reconcile because the resource-finalizer makes sure that the resource is only deleted when
			// it is not referenced by any deployer anymore. So, when the resource is already marked for deletion, we
			// want to get notified about deployer changes (e.g. deletion) to remove the resource-finalizer
			// respectively.
			&v1alpha1.Deployer{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				deployer, ok := obj.(*v1alpha1.Deployer)
				if !ok {
					return []reconcile.Request{}
				}

				resource := &v1alpha1.Resource{}
				if err := r.Get(ctx, client.ObjectKey{
					Namespace: deployer.Spec.ResourceRef.Namespace,
					Name:      deployer.Spec.ResourceRef.Name,
				}, resource); err != nil {
					return []reconcile.Request{}
				}

				// Only reconcile if the resource is marked for deletion
				if resource.GetDeletionTimestamp().IsZero() {
					return []reconcile.Request{}
				}

				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{
						Namespace: resource.GetNamespace(),
						Name:      resource.GetName(),
					}},
				}
			})).
		Complete(r)
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=resources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=resources/status,verbs=get;update;patch

//nolint:gocyclo,cyclop,funlen,gocognit,maintidx // we do not want to cut the function at arbitrary points
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("starting reconciliation")

	resource := &v1alpha1.Resource{}
	if err := r.Get(ctx, req.NamespacedName, resource); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper := patch.NewSerialPatcher(resource, r.Client)
	defer func(ctx context.Context) {
		err = status.UpdateStatus(ctx, patchHelper, resource, r.EventRecorder, resource.GetRequeueAfter(), err)
	}(ctx)

	logger.Info("preparing reconciling resource")
	if resource.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	if !resource.GetDeletionTimestamp().IsZero() {
		// The resource should only be deleted if no deployer exists that references that resource.
		deployerList := &v1alpha1.DeployerList{}
		if err := r.List(ctx, deployerList, &client.ListOptions{
			FieldSelector: fields.OneTermEqualSelector(
				deployerIndex,
				client.ObjectKeyFromObject(resource).String(),
			),
		}); err != nil {
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.DeletionFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to list deployers: %w", err)
		}

		if len(deployerList.Items) > 0 {
			var names []string
			for _, deployer := range deployerList.Items {
				names = append(names, deployer.Name)
			}

			msg := fmt.Sprintf(
				"resource cannot be removed as deployers are still referencing it: %s",
				strings.Join(names, ","),
			)
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.DeletionFailedReason, msg)

			return ctrl.Result{}, errors.New(msg)
		}

		if updated := controllerutil.RemoveFinalizer(resource, v1alpha1.ComponentFinalizer); updated {
			if err := r.Update(ctx, resource); err != nil {
				status.MarkNotReady(r.EventRecorder, resource, v1alpha1.DeletionFailedReason, err.Error())

				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}

			return ctrl.Result{}, nil
		}

		status.MarkNotReady(
			r.EventRecorder,
			resource,
			v1alpha1.DeletionFailedReason,
			"resource is being deleted and still has existing finalizers",
		)

		return ctrl.Result{}, nil
	}

	if updated := controllerutil.AddFinalizer(resource, v1alpha1.ComponentFinalizer); updated {
		if err := r.Update(ctx, resource); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}

		return ctrl.Result{Requeue: true}, nil
	}

	component, err := util.GetReadyObject[v1alpha1.Component, *v1alpha1.Component](ctx, r.Client, client.ObjectKey{
		Namespace: resource.GetNamespace(),
		Name:      resource.Spec.ComponentRef.Name,
	})
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.ResourceIsNotAvailable, err.Error())

		if errors.Is(err, util.NotReadyError{}) || errors.Is(err, util.DeletionError{}) {
			logger.Info("component is not available", "error", err)

			// return no requeue as we watch the object for changes anyway
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get ready component: %w", err)
	}

	logger.Info("reconciling resource")
	octx := ocmctx.New(datacontext.MODE_EXTENDED)
	defer func() {
		err = octx.Finalize()
	}()

	session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

	configs, err := ocm.GetEffectiveConfig(ctx, r.GetClient(), resource)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), resource, v1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get effective config: %w", err)
	}

	// If the component holds verification information, we need to add it to the ocm context
	verifications, err := ocm.GetVerifications(ctx, r.GetClient(), component)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), resource, v1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get verifications: %w", err)
	}

	err = ocm.ConfigureContext(ctx, octx, r.GetClient(), configs, verifications)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), resource, v1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to configure context: %w", err)
	}

	spec, err := octx.RepositorySpecForConfig(component.Status.Component.RepositorySpec.Raw, nil)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), resource, v1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get repository spec: %w", err)
	}

	repo, err := session.LookupRepository(octx, spec)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), resource, v1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to lookup repository: %w", err)
	}

	cv, err := session.LookupComponentVersion(repo, component.Status.Component.Component, component.Status.Component.Version)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to lookup component version: %w", err)
	}

	// Assuming the component descriptors are cached by the verification in the component controller, the verification
	// is the only thing we do twice. Or is this omitted when the component is pulled from the ocm cache?
	cds, err := ocm.VerifyComponentVersionAndListDescriptors(ctx, octx, cv, sliceutils.Transform(component.Spec.Verify, func(verify v1alpha1.Verification) string {
		return verify.Signature
	}))
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to list verified descriptors: %w", err)
	}

	resourceReference := v1.ResourceReference{
		Resource:      resource.Spec.Resource.ByReference.Resource,
		ReferencePath: resource.Spec.Resource.ByReference.ReferencePath,
	}

	resourceAccess, resourceCompDesc, err := ocm.GetResourceAccessForComponentVersion(cv, resourceReference, cds)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetOCMResourceFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get resource access: %w", err)
	}

	accSpec, err := resourceAccess.Access()
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetOCMResourceFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get resource access spec: %w", err)
	}

	sourceRef := &v1alpha1.SourceReference{}

	switch access := accSpec.(type) {
	case *ociartifact.AccessSpec:
		ociURLDigest, err := access.GetOCIReference(cv)
		if err != nil {
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetReferenceFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to get OCI reference: %w", err)
		}

		ociURL, err := url.Parse(strings.Split(ociURLDigest, "@")[0])
		if err != nil {
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetReferenceFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to parse OCI reference URL: %w", err)
		}

		// gitHubURL.Parse will not acknowledge a hostname if a scheme is missing. But we cannot make sure that the reference
		// has a scheme.
		if ociURL.Host == "" {
			ociURL.Host = strings.Split(ociURL.Path, "/")[0]
			ociURL.Path = strings.TrimLeft(ociURL.Path, ociURL.Host+"/")
		}

		reference, err := name.ParseReference(fmt.Sprintf("%s/%s", ociURL.Host, ociURL.Path))
		if err != nil {
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetReferenceFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to parse OCI reference: %w", err)
		}

		sourceRef.Registry = reference.Context().RegistryStr()
		sourceRef.Repository = strings.TrimLeft(reference.Context().RepositoryStr(), "/")
		sourceRef.Reference = reference.Identifier()
	case *helm.AccessSpec:
		sourceRef.Registry = access.HelmRepository
		sourceRef.Repository = access.HelmChart
		sourceRef.Reference = access.GetVersion()
	case *github.AccessSpec:
		gitHubURL, err := giturls.Parse(access.RepoURL)
		if err != nil {
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetReferenceFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to parse GitHub URL: %w", err)
		}
		sourceRef.Registry = fmt.Sprintf("%s://%s", gitHubURL.Scheme, gitHubURL.Host)
		sourceRef.Repository = gitHubURL.Path
		sourceRef.Reference = access.Commit
	case *git.AccessSpec:
		gitURL, err := giturls.Parse(access.Repository)
		if err != nil {
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetReferenceFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to parse Git URL: %w", err)
		}

		sourceRef.Registry = fmt.Sprintf("%s://%s", gitURL.Scheme, gitURL.Host)
		sourceRef.Repository = gitURL.Path
		sourceRef.Reference = access.Ref
	default:
		logger.Info("skip setting reference for resource as no source reference is available for this access type", "access type", access)
	}

	// Get repository spec of actual component descriptor of the referenced resource
	resolver := resolvers.NewCompoundResolver(repo, octx.GetResolver())
	resCompVers, err := session.LookupComponentVersion(resolver, resourceCompDesc.GetName(), resourceCompDesc.GetVersion())
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get component version of resource: %w", err)
	}
	resCompVersRepoSpec := resCompVers.Repository().GetSpecification()

	resCompVersRepoSpecData, err := json.Marshal(resCompVersRepoSpec)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.MarshalFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to marshal resource spec: %w", err)
	}

	// Update status
	if err = setResourceStatus(ctx, configs, resource, resourceAccess, sourceRef, &v1alpha1.ComponentInfo{
		RepositorySpec: &apiextensionsv1.JSON{Raw: resCompVersRepoSpecData},
		Component:      resourceCompDesc.GetName(),
		Version:        resourceCompDesc.GetVersion(),
	}); err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.StatusSetFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to set resource status: %w", err)
	}

	status.MarkReady(r.EventRecorder, resource, "Applied version %s", resourceAccess.Meta().GetVersion())

	return ctrl.Result{RequeueAfter: resource.GetRequeueAfter()}, nil
}

// setResourceStatus updates the resource status with the all required information.
func setResourceStatus(
	ctx context.Context,
	configs []v1alpha1.OCMConfiguration,
	resource *v1alpha1.Resource,
	resourceAccess ocmctx.ResourceAccess,
	reference *v1alpha1.SourceReference,
	component *v1alpha1.ComponentInfo,
) error {
	log.FromContext(ctx).V(1).Info("updating resource status")

	// Get the access spec from the resource access
	accessSpec, err := resourceAccess.Access()
	if err != nil {
		return fmt.Errorf("failed to get access spec: %w", err)
	}

	accessData, err := json.Marshal(accessSpec)
	if err != nil {
		return fmt.Errorf("failed to marshal access spec: %w", err)
	}

	resource.Status.Resource = &v1alpha1.ResourceInfo{
		Name:          resourceAccess.Meta().Name,
		Type:          resourceAccess.Meta().Type,
		Version:       resourceAccess.Meta().Version,
		ExtraIdentity: resourceAccess.Meta().ExtraIdentity,
		Access:        apiextensionsv1.JSON{Raw: accessData},
		Digest:        resourceAccess.Meta().Digest.String(),
	}

	resource.Status.EffectiveOCMConfig = configs

	resource.Status.Reference = reference
	resource.Status.Component = component

	return nil
}

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

	giturls "github.com/chainguard-dev/git-urls"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/mandelsoft/goutils/sliceutils"
	"k8s.io/apimachinery/pkg/types"
	"ocm.software/ocm/api/datacontext"
	"ocm.software/ocm/api/ocm/compdesc"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/git"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/helm"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
	"ocm.software/ocm/api/ocm/resolvers"
	"ocm.software/ocm/api/ocm/selectors"
	"ocm.software/ocm/api/ocm/tools/signing"
	"ocm.software/ocm/api/utils/blobaccess"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	ocmctx "ocm.software/ocm/api/ocm"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

type Reconciler struct {
	*ocm.BaseReconciler
}

var _ ocm.Reconciler = (*Reconciler)(nil)

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
		Complete(r)
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=resources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=resources/status,verbs=get;update;patch

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	resource := &v1alpha1.Resource{}
	if err := r.Get(ctx, req.NamespacedName, resource); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileWithStatusUpdate(ctx, resource)
}

func (r *Reconciler) reconcileWithStatusUpdate(ctx context.Context, resource *v1alpha1.Resource) (ctrl.Result, error) {
	patchHelper := patch.NewSerialPatcher(resource, r.Client)

	result, err := r.reconcileExists(ctx, resource)

	err = errors.Join(err, status.UpdateStatus(ctx, patchHelper, resource, r.EventRecorder, resource.GetRequeueAfter(), err))
	if err != nil {
		return ctrl.Result{}, err
	}

	return result, nil
}

func (r *Reconciler) reconcileExists(ctx context.Context, resource *v1alpha1.Resource) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("preparing reconciling resource")

	if resource.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	if !resource.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, errors.New("resource is being deleted")
	}

	return r.reconcile(ctx, resource)
}

func (r *Reconciler) reconcile(ctx context.Context, resource *v1alpha1.Resource) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	// Get component to resolve resource from component descriptor and verify digest
	component := &v1alpha1.Component{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: resource.GetNamespace(),
		Name:      resource.Spec.ComponentRef.Name,
	}, component); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get component: %w", err)
	}

	// Check if component is applicable or is getting deleted/not ready
	if !component.GetDeletionTimestamp().IsZero() {
		logger.Error(errors.New("component is being deleted"), "component is being deleted and therefore, cannot be used")

		return ctrl.Result{}, nil
	}

	if !conditions.IsReady(component) {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.ComponentIsNotReadyReason, "Component is not ready")

		return ctrl.Result{}, nil
	}

	return r.reconcileOCM(ctx, resource, component)
}

func (r *Reconciler) reconcileOCM(ctx context.Context, resource *v1alpha1.Resource, component *v1alpha1.Component) (ctrl.Result, error) {
	octx := ocmctx.New(datacontext.MODE_EXTENDED)

	result, err := r.reconcileResource(ctx, octx, resource, component)

	// Always finalize ocm context after reconciliation
	err = errors.Join(err, octx.Finalize())
	if err != nil {
		// this should be retryable, as it is difficult to forsee whether
		// another error condition might lead to problems closing the ocm
		// context
		return ctrl.Result{}, err
	}

	return result, nil
}

//nolint:funlen,cyclop,maintidx // we do not want to cut function at an arbitrary point
func (r *Reconciler) reconcileResource(ctx context.Context, octx ocmctx.Context, resource *v1alpha1.Resource, component *v1alpha1.Component) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("reconciling resource")

	session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

	configs, err := ocm.GetEffectiveConfig(ctx, r.GetClient(), resource)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), resource, v1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	err = ocm.ConfigureContext(ctx, octx, r.GetClient(), configs)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), resource, v1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	// TODO: Get descriptor lists
	spec, err := octx.RepositorySpecForConfig(component.Status.Component.RepositorySpec.Raw, nil)
	if err != nil {
		logger.Error(err, "failed to parse repository spec")
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.RepositorySpecInvalidReason, "RepositorySpec is invalid")

		return ctrl.Result{}, err
	}

	repo, err := session.LookupRepository(octx, spec)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.RepositorySpecInvalidReason, "RepositorySpec is invalid")

		return ctrl.Result{}, fmt.Errorf("invalid repository spec: %w", err)
	}

	cv, err := session.LookupComponentVersion(repo, component.Status.Component.Component, component.Status.Component.Version)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get component version: %w", err)
	}

	// Assuming the component descriptors are cached by the verification in the component controller, the verification
	// is the only thing we do twice. Or is this omitted when the component is pulled from the ocm cache?
	cds, err := ocm.VerifyComponentVersion(ctx, cv, sliceutils.Transform(component.Spec.Verify, func(verify v1alpha1.Verification) string {
		return verify.Signature
	}))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list verified descriptors: %w", err)
	}

	cdSet := compdesc.NewComponentVersionSet(cds.List...)

	// Get referenced component descriptor from component descriptor set
	cd, err := cdSet.LookupComponentVersion(component.Status.Component.Component, component.Status.Component.Version)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetComponentDescriptorsFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to lookup component descriptor: %w", err)
	}

	// Get resource, respective component descriptor and component version
	resourceReference := v1.ResourceReference{
		Resource:      resource.Spec.Resource.ByReference.Resource,
		ReferencePath: resource.Spec.Resource.ByReference.ReferencePath,
	}

	// Resolve resource resourceReference to get resource and its component descriptor
	resourceDesc, resourceCompDesc, err := compdesc.ResolveResourceReference(cd, resourceReference, cdSet)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.ResolveResourceFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to resolve resource reference: %w", err)
	}

	resourceAccess, err := getResourceAccess(ctx, cv, resourceDesc, resourceCompDesc)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetResourceAccessFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	// Consider option to omit verification (= resource download) if the resource is large
	if err := verifyResource(ctx, resourceAccess, cv, cd); err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.VerifyResourceFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	accSpec, err := resourceAccess.Access()
	if err != nil {
		return ctrl.Result{}, err
	}

	sourceRef := &v1alpha1.SourceReference{}

	switch access := accSpec.(type) {
	case *ociartifact.AccessSpec:
		ociRef, err := access.GetOCIReference(cv)
		if err != nil {
			return ctrl.Result{}, err
		}

		reference, err := name.ParseReference(ociRef)
		if err != nil {
			return ctrl.Result{}, err
		}

		sourceRef.Registry = reference.Context().RegistryStr()
		sourceRef.Repository = reference.Context().RepositoryStr()
		sourceRef.Reference = reference.Identifier()
	case *helm.AccessSpec:
		sourceRef.Registry = access.HelmRepository
		sourceRef.Repository = access.HelmChart
		sourceRef.Reference = access.GetVersion()
	case *git.AccessSpec:
		url, err := giturls.Parse(access.Repository)
		if err != nil {
			return ctrl.Result{}, err
		}

		sourceRef.Registry = fmt.Sprintf("%s://%s", url.Scheme, url.Host)
		sourceRef.Repository = url.Path
		sourceRef.Reference = access.Ref
	default:
		return ctrl.Result{}, fmt.Errorf("unsupported access spec type: %T", access)
	}

	// Update status
	if err = setResourceStatus(ctx, configs, resource, resourceAccess, sourceRef); err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.StatusSetFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to set resource status: %w", err)
	}

	status.MarkReady(r.EventRecorder, resource, "Applied version %s", resourceAccess.Meta().GetVersion())

	return ctrl.Result{RequeueAfter: resource.GetRequeueAfter()}, nil
}

// getComponentVersion returns the component version for the given component descriptor.
func getComponentVersion(ctx context.Context, octx ocmctx.Context, session ocmctx.Session, spec []byte, compDesc *compdesc.ComponentDescriptor) (
	ocmctx.ComponentVersionAccess, error,
) {
	log.FromContext(ctx).V(1).Info("getting component version")

	// Get repository and resolver to get the respective component version of the resource
	repoSpec, err := octx.RepositorySpecForConfig(spec, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get repository spec: %w", err)
	}
	repo, err := session.LookupRepository(octx, repoSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup repository: %w", err)
	}

	resolver := resolvers.NewCompoundResolver(repo, octx.GetResolver())

	// Get component version for resource access
	cv, err := session.LookupComponentVersion(resolver, compDesc.Name, compDesc.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup component version: %w", err)
	}

	return cv, nil
}

// getResourceAccess returns the resource access for the given resource and component descriptor from the component version access.
func getResourceAccess(ctx context.Context, cv ocmctx.ComponentVersionAccess, resourceDesc *compdesc.Resource, compDesc *compdesc.ComponentDescriptor) (ocmctx.ResourceAccess, error) {
	log.FromContext(ctx).V(1).Info("get resource access")

	resAccesses, err := cv.SelectResources(selectors.Identity(resourceDesc.GetIdentity(compDesc.GetResources())))
	if err != nil {
		return nil, fmt.Errorf("failed to select resources: %w", err)
	}

	var resourceAccess ocmctx.ResourceAccess
	switch len(resAccesses) {
	case 0:
		return nil, errors.New("no resources selected")
	case 1:
		resourceAccess = resAccesses[0]
	default:
		return nil, errors.New("cannot determine the resource access unambiguously")
	}

	return resourceAccess, nil
}

// getBlobAccess returns the blob access for the given resource access.
func getBlobAccess(ctx context.Context, access ocmctx.ResourceAccess) (blobaccess.BlobAccess, error) {
	log.FromContext(ctx).V(1).Info("get resource blob access")

	// Create data access
	accessMethod, err := access.AccessMethod()
	if err != nil {
		return nil, fmt.Errorf("failed to create access method: %w", err)
	}

	return accessMethod.AsBlobAccess(), nil
}

// verifyResource verifies the resource digest with the digest from the component version access and component descriptor.
func verifyResource(ctx context.Context, access ocmctx.ResourceAccess, cv ocmctx.ComponentVersionAccess, cd *compdesc.ComponentDescriptor) error {
	logger := log.FromContext(ctx)
	logger.V(1).Info("verify resource")

	// TODO: https://github.com/open-component-model/ocm-k8s-toolkit/issues/71
	index := cd.GetResourceIndex(access.Meta())
	if index < 0 {
		return errors.New("resource not found in access spec")
	}
	raw := &cd.Resources[index]
	if raw.Digest == nil {
		logger.V(1).Info("no resource-digest in descriptor found. Skipping verification")

		return nil
	}

	blobAccess, err := getBlobAccess(ctx, access)
	if err != nil {
		return err
	}

	// Add the component descriptor to the local verified store, so its digest will be compared with the digest from the
	// component version access
	store := signing.NewLocalVerifiedStore()
	store.Add(cd)

	ok, err := signing.VerifyResourceDigestByResourceAccess(cv, access, blobAccess, store)
	if !ok {
		if err != nil {
			return fmt.Errorf("verification failed: %w", err)
		}

		return errors.New("expected signature verification to be relevant, but it was not")
	}
	if err != nil {
		return fmt.Errorf("failed to verify resource digest: %w", err)
	}

	return nil
}

// setResourceStatus updates the resource status with the all required information.
func setResourceStatus(ctx context.Context, configs []v1alpha1.OCMConfiguration, resource *v1alpha1.Resource, resourceAccess ocmctx.ResourceAccess, reference *v1alpha1.SourceReference) error {
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

	return nil
}

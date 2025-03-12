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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/opencontainers/go-digest"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"ocm.software/ocm/api/datacontext"
	"ocm.software/ocm/api/ocm/compdesc"
	"ocm.software/ocm/api/ocm/resolvers"
	"ocm.software/ocm/api/ocm/selectors"
	"ocm.software/ocm/api/ocm/tools/signing"
	"ocm.software/ocm/api/utils/blobaccess"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	ocmctx "ocm.software/ocm/api/ocm"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/compression"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

type Reconciler struct {
	*ocm.BaseReconciler
	Registry ociartifact.RegistryType
}

var _ ocm.Reconciler = (*Reconciler)(nil)

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create index for component reference name from resources
	const fieldName = "spec.componentRef.name"
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.Resource{}, fieldName, func(obj client.Object) []string {
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

	if resource.GetDeletionTimestamp() != nil {
		if err := ociartifact.DeleteForObject(ctx, r.Registry, resource); err != nil {
			return ctrl.Result{}, err
		}

		if updated := controllerutil.RemoveFinalizer(resource, v1alpha1.ArtifactFinalizer); updated {
			if err := r.Update(ctx, resource); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}

		logger.Info("resource is being deleted and cannot be used", "name", resource.Name)

		return ctrl.Result{Requeue: true}, nil
	}

	if updated := controllerutil.AddFinalizer(resource, v1alpha1.ArtifactFinalizer); updated {
		if err := r.Update(ctx, resource); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}

		return ctrl.Result{Requeue: true}, nil
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
	if component.GetDeletionTimestamp() != nil {
		logger.Error(errors.New("component is being deleted"), "component is being deleted and therefore, cannot be used")

		return ctrl.Result{}, nil
	}

	if !conditions.IsReady(component) {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.ComponentIsNotReadyReason, "Component is not ready")

		return ctrl.Result{}, errors.New("component is not ready")
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

//nolint:funlen,cyclop // we do not want to cut function at an arbitrary point
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

	// Create repository to download the component descriptors
	repositoryCD, err := r.Registry.NewRepository(ctx, component.GetOCIRepository())
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), resource, v1alpha1.CreateOCIRepositoryFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	// Get component descriptor set from artifact
	data, err := repositoryCD.FetchArtifact(ctx, component.GetManifestDigest())
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), resource, v1alpha1.FetchOCIArtifactFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	cds := &ocm.Descriptors{}
	if err := yaml.NewYAMLToJSONDecoder(bytes.NewReader(data)).Decode(cds); err != nil {
		status.MarkNotReady(r.GetEventRecorder(), resource, v1alpha1.YamlToJSONDecodeFailedReason, err.Error())

		return ctrl.Result{}, err
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

	cv, err := getComponentVersion(ctx, octx, session, component.Status.Component.RepositorySpec.Raw, resourceCompDesc)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	resourceAccess, err := getResourceAccess(ctx, cv, resourceDesc, resourceCompDesc)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetResourceAccessFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	if err := verifyResource(ctx, resourceAccess, cv, cd); err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.VerifyResourceFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	// TODO:
	//   Problem: The current implementation would download the resource every reconcile-loop. This could be
	//   expensive depending on the resource.
	//   Additionally, there could be two different components that contain the same resource. Also in this case
	//   we would re-download the resource, even though it is already in the OCI registry.
	//   To circumvent this problem, we could use the digest provided by the resource-access (prior to the resource
	//   download) as the repository-name. Then, we could check if such a repository for the resource already
	//   exists.
	//   If so, we cannot not return immediately because we need to create a manifest-file to point to the already
	//   present resource-layer. Otherwise the GC would delete the resource-layer if the previously present manifest
	//   would be deleted.

	ociRepositoryName, err := ociartifact.CreateRepositoryName(resource.Spec.ComponentRef.Name, resource.GetName())
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), resource, v1alpha1.CreateOCIRepositoryNameFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	ociRepository, err := r.Registry.NewRepository(ctx, ociRepositoryName)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), resource, v1alpha1.CreateOCIRepositoryFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	var (
		manifestDigest digest.Digest
		blobSize       int64
	)

	// If the resource is of type 'ociArtifact' or its access type is 'ociArtifact', the resource will be copied to the
	// internal OCI registry
	logger.Info("create OCI artifact for resource", "name", resource.GetName(), "type", resourceAccess.Meta().GetType())
	resourceAccessSpec, err := resourceAccess.Access()
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetResourceAccessFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	if resourceAccessSpec == nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetResourceAccessFailedReason, "access spec is nil")

		return ctrl.Result{}, err
	}

	tag := resourceAccess.Meta().GetVersion()

	if resourceAccessSpec.GetType() == "ociArtifact" {
		manifestDigest, err = ociRepository.CopyOCIArtifactForResourceAccess(ctx, resourceAccess)
		if err != nil {
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.CopyOCIArtifactFailedReason, err.Error())

			return ctrl.Result{}, err
		}

		// TODO: How to get the blob size, without downloading the resource?
		//  Do we need the blob-size, when we copy the resource either way?
		//  We could use the size stored in the manifest.
		blobSize = 0
	} else {
		// Get resource content
		// No need to close the blob access as it will be closed automatically
		blobAccess, err := getBlobAccess(ctx, resourceAccess)
		if err != nil {
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetBlobAccessFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to get blob access: %w", err)
		}

		resourceContent, err := blobAccess.Get()
		if err != nil {
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetResourceFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to get blob: %w", err)
		}

		// Compress content to gzip if it is not already compressed to avoid to large blobs
		resourceContentCompressed, err := compression.AutoCompressAsGzip(ctx, resourceContent)
		if err != nil {
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.CompressGzipFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to auto compress data: %w", err)
		}

		tag = ocm.NormalizeVersion(tag)
		manifestDigest, err = ociRepository.PushArtifact(ctx, tag, resourceContentCompressed)
		if err != nil {
			status.MarkNotReady(r.GetEventRecorder(), resource, v1alpha1.PushOCIArtifactFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to push OCI artifact: %w", err)
		}

		blobSize = int64(len(resourceContentCompressed))
	}

	// Delete previous artifact version, if any. Note that resource's status isn't updated yet.
	err = ociartifact.DeleteIfDigestMismatch(ctx, r.Registry, resource, manifestDigest)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.DeleteOCIArtifactFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	// Update status
	if err = setResourceStatus(ctx, configs, resource, resourceAccess, &v1alpha1.OCIArtifactInfo{
		Repository: ociRepositoryName,
		Digest:     manifestDigest.String(),
		Blob: &v1alpha1.BlobInfo{
			Digest: resourceAccess.Meta().Digest.Value,
			Tag:    tag,
			Size:   blobSize,
		},
	}); err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.StatusSetFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to set resource status: %w", err)
	}

	status.MarkReady(r.EventRecorder, resource, "Applied version %s", resourceAccess.Meta().Version)

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
func setResourceStatus(ctx context.Context, configs []v1alpha1.OCMConfiguration, resource *v1alpha1.Resource, resourceAccess ocmctx.ResourceAccess, ociArtifact *v1alpha1.OCIArtifactInfo) error {
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

	resource.Status.OCIArtifact = ociArtifact

	return nil
}

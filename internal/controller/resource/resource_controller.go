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
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	errors2 "github.com/mandelsoft/goutils/errors"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"ocm.software/ocm/api/datacontext"
	ocmctx "ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/compdesc"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/extensions/download"
	"ocm.software/ocm/api/ocm/resolvers"
	"ocm.software/ocm/api/ocm/selectors"
	"ocm.software/ocm/api/ocm/tools/signing"
	"ocm.software/ocm/api/utils/blobaccess"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/compression"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

type Reconciler struct {
	*ocm.BaseReconciler
	Storage *storage.Storage
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
		// Watch for artifacts-events that are owned by the resource controller
		Owns(&artifactv1.Artifact{}).
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
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=resources/finalizers,verbs=update

// +kubebuilder:rbac:groups=openfluxcd.ocm.software,resources=artifacts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openfluxcd.ocm.software,resources=artifacts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openfluxcd.ocm.software,resources=artifacts/finalizers,verbs=update

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
		// TODO: This is a temporary solution until a artifact-reconciler is written to handle the deletion of artifacts
		if err := ocm.RemoveArtifactForCollectable(ctx, r.Client, r.Storage, resource); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove artifact: %w", err)
		}

		if removed := controllerutil.RemoveFinalizer(resource, v1alpha1.ArtifactFinalizer); removed {
			if err := r.Update(ctx, resource); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}

		return ctrl.Result{}, nil
	}

	if added := controllerutil.AddFinalizer(resource, v1alpha1.ArtifactFinalizer); added {
		err := r.Update(ctx, resource)
		if err != nil {
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

func (r *Reconciler) reconcileResource(ctx context.Context, octx ocmctx.Context, resource *v1alpha1.Resource, component *v1alpha1.Component) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("reconciling resource")

	session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

	if err := ocm.ConfigureOCMContext(ctx, r, octx, resource, component); err != nil {
		return ctrl.Result{}, err
	}

	// Get artifact from component that contains component descriptor
	artifactComponent := &artifactv1.Artifact{}
	if err := r.Get(ctx, types.NamespacedName{
		// TODO: see https://github.com/open-component-model/ocm-project/issues/295
		Namespace: resource.GetNamespace(),
		Name:      component.Status.ArtifactRef.Name,
	}, artifactComponent); err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetArtifactFailedReason, "Cannot get component artifact")

		return ctrl.Result{}, fmt.Errorf("failed to get component artifact: %w", err)
	}

	// Get component descriptor set from artifact
	cdSet, err := ocm.GetComponentSetForArtifact(r.Storage, artifactComponent)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetComponentForArtifactFailedReason, err.Error())

		return ctrl.Result{}, err
	}

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

	// revision is the digest of the resource. It is used to identify the resource in the storage (as filename) and to
	// check if the resource is already present in the storage.
	revision := resourceAccess.Meta().Digest.Value

	// Get the artifact to check if it is already present while reconciling it
	artifactStorage := r.Storage.NewArtifactFor(resource.GetKind(), resource.GetObjectMeta(), "", "")
	if err := r.Client.Get(ctx, types.NamespacedName{Name: artifactStorage.Name, Namespace: artifactStorage.Namespace}, artifactStorage); err != nil {
		if !apierrors.IsNotFound(err) {
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetArtifactFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to get artifactStorage: %w", err)
		}
	}

	err = reconcileArtifact(ctx, octx, r.Storage, resource, resourceAccess, revision, artifactStorage, func() error { return verifyResource(ctx, resourceAccess, cv, cd) })
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.ReconcileArtifactFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	// Update status
	if err = setResourceStatus(ctx, resource, resourceAccess); err != nil {
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.StatusSetFailedReason, err.Error())

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
	log.FromContext(ctx).V(1).Info("verify resource")

	// TODO: https://github.com/open-component-model/ocm-k8s-toolkit/issues/71
	index := cd.GetResourceIndex(access.Meta())
	if index < 0 {
		return errors2.ErrNotFound("resource")
	}
	raw := &cd.Resources[index]
	if raw.Digest == nil {
		log.FromContext(ctx).V(1).Info("no resource-digest in descriptor found. Skipping verification")

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

// downloadResource downloads the resource from the resource access.
func downloadResource(ctx context.Context, octx ocmctx.Context, targetDir string, resource *v1alpha1.Resource, acc ocmctx.ResourceAccess, bAcc blobaccess.BlobAccess,
) (string, error) {
	log.FromContext(ctx).V(1).Info("download resource")

	// Using a redirected resource acc to prevent redundant download
	accessMock, err := ocm.NewRedirectedResourceAccess(acc, bAcc)
	if err != nil {
		return "", fmt.Errorf("failed to create redirected resource acc: %w", err)
	}

	path, err := download.DownloadResource(octx, accessMock, filepath.Join(targetDir, resource.Name))
	if err != nil {
		return "", fmt.Errorf("failed to download resource: %w", err)
	}

	return path, nil
}

// reconcileArtifact will download, verify, and reconcile the artifact in the storage if it is not already present in the storage.
// TODO: https://github.com/open-component-model/ocm-project/issues/297
func reconcileArtifact(
	ctx context.Context,
	octx ocmctx.Context,
	storage *storage.Storage,
	resource *v1alpha1.Resource,
	acc ocmctx.ResourceAccess,
	revision string,
	artifact *artifactv1.Artifact,
	verifyFunc func() error,
) (retErr error) {
	log.FromContext(ctx).V(1).Info("reconcile artifact")

	// Check if the artifact is already present and located in the storage
	localPath := storage.LocalPath(artifact)

	// use the filename which is the revision as the artifact name
	artifactPresent := storage.ArtifactExist(artifact) && strings.Split(filepath.Base(localPath), ".")[0] == revision

	// Init variables with default values in case the artifact is present
	// If the artifact is present, the dirPath will be the directory of the local path to the directory
	dirPath := filepath.Dir(localPath)
	// If the artifact is already present, we do not want to archive it again
	archiveFunc := func(_ *artifactv1.Artifact, _ string) error {
		return nil
	}

	// If the artifact is not present, we will verify and download the resource and provide it as artifact
	//nolint:nestif // this is our main logic and we rather keep it in here
	if !artifactPresent {
		// No need to close the blob access as it will be closed automatically
		bAcc, err := getBlobAccess(ctx, acc)
		if err != nil {
			return err
		}

		// Check if resource can be verified
		if err := verifyFunc(); err != nil {
			return err
		}

		// Target directory in which the resource is downloaded
		tmp, err := os.MkdirTemp("", "resource-*")
		if err != nil {
			return fmt.Errorf("failed to create temporary directory: %w", err)
		}
		defer func() {
			retErr = errors.Join(retErr, os.RemoveAll(tmp))
		}()

		path, err := downloadResource(ctx, octx, tmp, resource, acc, bAcc)
		if err != nil {
			return err
		}

		// Since the artifact is not already present, an archive function is added to archive the downloaded resource in the storage
		archiveFunc = func(art *artifactv1.Artifact, _ string) error {
			logger := log.FromContext(ctx).WithValues("artifact", art.Name, "revision", revision, "path", path)
			fi, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("failed to get file info: %w", err)
			}
			if fi.IsDir() {
				logger.V(1).Info("archiving directory")
				// Archive directory to storage
				if err := storage.Archive(art, path, nil); err != nil {
					return fmt.Errorf("failed to archive: %w", err)
				}
			} else {
				if err := compression.AutoCompressAsGzipAndArchiveFile(ctx, art, storage, path); err != nil {
					return fmt.Errorf("failed to auto compress and archive file: %w", err)
				}
			}

			resource.Status.ArtifactRef = corev1.LocalObjectReference{
				Name: art.Name,
			}

			return nil
		}

		// Overwrite the default dirPath with the temporary directory path that points to the downloaded resource
		dirPath = tmp
	}

	if err := storage.ReconcileStorage(ctx, resource); err != nil {
		return fmt.Errorf("failed to reconcile resource storage: %w", err)
	}

	// Provide artifact in storage
	if err := storage.ReconcileArtifact(ctx, resource, revision, dirPath, revision, archiveFunc); err != nil {
		return fmt.Errorf("failed to reconcile resource artifact: %w", err)
	}

	return nil
}

// setResourceStatus updates the resource status with the all required information.
func setResourceStatus(ctx context.Context, resource *v1alpha1.Resource, resourceAccess ocmctx.ResourceAccess) error {
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

	resource.Status.ConfigRefs = slices.Clone(resource.Spec.ConfigRefs)
	resource.Status.SecretRefs = slices.Clone(resource.Spec.SecretRefs)

	return nil
}

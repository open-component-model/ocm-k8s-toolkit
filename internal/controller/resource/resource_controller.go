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
	"slices"
	"strings"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/mandelsoft/filepath/pkg/filepath"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
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
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/rerror"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

type Reconciler struct {
	*ocm.BaseReconciler
	Storage *storage.Storage
}

var _ ocm.Reconciler = (*Reconciler)(nil)

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Resource{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=resources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=resources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=resources/finalizers,verbs=update

// +kubebuilder:rbac:groups=openfluxcd.mandelsoft.org,resources=artifacts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openfluxcd.mandelsoft.org,resources=artifacts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openfluxcd.mandelsoft.org,resources=artifacts/finalizers,verbs=update

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	resource := &v1alpha1.Resource{}
	if err := r.Get(ctx, req.NamespacedName, resource); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return rerror.EvaluateReconcileError(r.reconcileExists(ctx, resource))
}

func (r *Reconciler) reconcileExists(ctx context.Context, resource *v1alpha1.Resource) (_ ctrl.Result, retErr rerror.ReconcileError) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("checking reconciling resource")

	if resource.GetDeletionTimestamp() != nil {
		logger.Info("deleting resource", "name", resource.Name)

		return ctrl.Result{}, nil
	}

	if resource.Spec.Suspend {
		logger.Info("resource is suspended, skipping reconciliation")

		return ctrl.Result{}, nil
	}

	return r.reconcilePrepare(ctx, resource)
}

func (r *Reconciler) reconcilePrepare(ctx context.Context, resource *v1alpha1.Resource) (ret ctrl.Result, retErr rerror.ReconcileError) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("preparing reconciling resource")

	patchHelper := patch.NewSerialPatcher(resource, r.Client)

	// Always attempt to patch the object and status after each reconciliation.
	defer func() {
		if err := status.UpdateStatus(ctx, patchHelper, resource, r.EventRecorder, resource.GetRequeueAfter(), retErr); err != nil {
			retErr = rerror.AsRetryableError(errors.Join(retErr, err))
			ret = ctrl.Result{}
		}
	}()

	// Get component to resolve resource from component descriptor and verify digest
	component := &v1alpha1.Component{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: resource.GetNamespace(),
		Name:      resource.Spec.ComponentRef.Name,
	}, component); err != nil {
		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to get component: %w", err))
	}

	// Check if component is usable
	if component.GetDeletionTimestamp() != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(errors.New("component is being deleted"))
	}

	if !conditions.IsReady(component) {
		logger.Info("component is not ready", "name", resource.Spec.ComponentRef.Name)
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.ComponentIsNotReadyReason, "Component is not ready")

		return ctrl.Result{}, rerror.AsRetryableError(errors.New("component is not ready"))
	}

	return r.reconcile(ctx, resource, component)
}

func (r *Reconciler) reconcile(ctx context.Context, resource *v1alpha1.Resource, component *v1alpha1.Component) (ret ctrl.Result, retErr rerror.ReconcileError) {
	log.FromContext(ctx).V(1).Info("reconciling resource")

	// DefaultContext is essentially the same as the extended context created here. The difference is, if we
	// register a new type at an extension point (e.g. a new access type), it's only registered at this exact context
	// instance and not at the global default context variable.
	octx := ocmctx.New(datacontext.MODE_EXTENDED)
	defer func() {
		// TODO: Discuss if this is non- or retryable error
		retErr = rerror.AsRetryableError(errors.Join(retErr, octx.Finalize()))
		if retErr != nil {
			ret = ctrl.Result{}
		}
	}()
	session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

	if retErr = ocm.ConfigureOCMContext(ctx, r, octx, resource, component); retErr != nil {
		return ctrl.Result{}, retErr
	}

	// Get storageArtifact from component that contains component descriptor
	artifactComponent := &artifactv1.Artifact{}
	if err := r.Get(ctx, types.NamespacedName{
		// TODO: Discuss if we should use OwnerReference instead of refs?
		Namespace: resource.GetNamespace(),
		Name:      component.Status.ArtifactRef.Name,
	}, artifactComponent); err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetArtifactFailedReason, "Cannot get component storageArtifact")

		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to get component storageArtifact: %w", err))
	}

	// Get component descriptor set
	cdSet, rErr := getComponentSetForArtifact(ctx, r.Storage, artifactComponent)
	if rErr != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetComponentForArtifactFailedReason, rErr.Error())

		return ctrl.Result{}, rErr
	}

	// Get referenced component descriptor
	cd, err := cdSet.LookupComponentVersion(component.Status.Component.Component, component.Status.Component.Version)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetComponentDescriptorsFailedReason, err.Error())

		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to lookup component descriptor: %w", err))
	}

	// Get resource, respective component descriptor and component version
	reference := v1.ResourceReference{
		Resource: resource.Spec.Resource.ByReference.Resource,
		// ReferencePath: resource.Spec.Resource.ByReference.ReferencePath,
	}

	resourceArtifact, resourceCd, err := compdesc.ResolveResourceReference(cd, reference, cdSet)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.ResolveResourceFailedReason, err.Error())

		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to resolve resource reference: %w", err))
	}

	cv, rErr := getComponentVersion(ctx, octx, session, component.Status.Component.RepositorySpec.Raw, resourceCd)
	if rErr != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetComponentVersionFailedReason, rErr.Error())

		return ctrl.Result{}, rErr
	}

	// Get resource access for identity
	resourceAccess, rErr := getResourceAccess(ctx, cv, resourceArtifact, resourceCd)
	if rErr != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetResourceAccessFailedReason, rErr.Error())

		return ctrl.Result{}, rErr
	}

	// Get storageArtifact to check if is already present in the storage
	revision := resourceAccess.Meta().Digest.Value

	storageArtifact := r.Storage.NewArtifactFor(resource.GetKind(), resource.GetObjectMeta(), "", "")
	if err := r.Client.Get(ctx, types.NamespacedName{Name: storageArtifact.Name, Namespace: storageArtifact.Namespace}, &storageArtifact); err != nil {
		if !apierrors.IsNotFound(err) {
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetArtifactFailedReason, err.Error())

			return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to get storageArtifact: %w", err))
		}
	}

	// reconcileArtifact will download, verify, and reconcile the artifact in the storage if it is not already present in the storage
	rErr = reconcileArtifact(ctx, octx, r.Storage, resource, resourceAccess, cv, cd, revision, storageArtifact)
	if rErr != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.ReconcileArtifactFailedReason, rErr.Error())

		return ctrl.Result{}, rErr
	}

	// Update status
	if err = setResourceStatus(ctx, resource, resourceAccess); err != nil {
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.StatusSetFailedReason, err.Error())

		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to set resource status: %w", err))
	}

	status.MarkReady(r.EventRecorder, resource, "Applied version %s", resourceAccess.Meta().Version)

	return ctrl.Result{RequeueAfter: resource.GetRequeueAfter()}, nil
}

func getComponentSetForArtifact(ctx context.Context, storage *storage.Storage, artifact *artifactv1.Artifact) (_ *compdesc.ComponentVersionSet, retErr rerror.ReconcileError) {
	log.FromContext(ctx).V(1).Info("getting component set")

	tmp, err := os.MkdirTemp("", "component-*")
	if err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to create temporary directory: %w", err))
	}
	defer func() {
		retErr = rerror.AsRetryableError(errors.Join(retErr, os.RemoveAll(tmp)))
	}()

	// Instead of using the http-functionality of the storage-server, we use the storage directly for performance reasons.
	// This assumes that the controllers and the storage are running in the same pod.
	unlock, err := storage.Lock(*artifact)
	if err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to lock artifact: %w", err))
	}
	defer unlock()

	filePath := filepath.Join(tmp, v1alpha1.OCMComponentDescriptorList)

	if err := storage.CopyToPath(artifact, v1alpha1.OCMComponentDescriptorList, filePath); err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to copy artifact to path: %w", err))
	}

	// Read component descriptor list
	file, err := os.Open(filepath.Join(tmp, v1alpha1.OCMComponentDescriptorList))
	if err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to open component descriptor: %w", err))
	}
	defer func() {
		retErr = rerror.AsRetryableError(errors.Join(retErr, file.Close()))
	}()

	// Get component descriptor set
	cds := &ocm.Descriptors{}
	const bufferSize = 4096
	decoder := yaml.NewYAMLOrJSONDecoder(file, bufferSize)
	if err := decoder.Decode(cds); err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to unmarshal component descriptors: %w", err))
	}

	return compdesc.NewComponentVersionSet(cds.List...), retErr
}

func getComponentVersion(ctx context.Context, octx ocmctx.Context, session ocmctx.Session, spec []byte, compDesc *compdesc.ComponentDescriptor) (
	ocmctx.ComponentVersionAccess, rerror.ReconcileError,
) {
	log.FromContext(ctx).V(1).Info("getting component version")

	// Get repository and resolver to get the respective component version of the resource
	repoSpec, err := octx.RepositorySpecForConfig(spec, nil)
	if err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to get repository spec: %w", err))
	}
	repo, err := session.LookupRepository(octx, repoSpec)
	if err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to lookup repository: %w", err))
	}

	resolver := resolvers.NewCompoundResolver(repo, octx.GetResolver())

	// Get component version for resource access
	cv, err := session.LookupComponentVersion(resolver, compDesc.Name, compDesc.Version)
	if err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to lookup component version: %w", err))
	}

	return cv, nil
}

func getResourceAccess(ctx context.Context, cv ocmctx.ComponentVersionAccess, resourceDesc *compdesc.Resource, compDesc *compdesc.ComponentDescriptor) (ocmctx.ResourceAccess, rerror.ReconcileError) {
	log.FromContext(ctx).V(1).Info("get resource access")

	resAccesses, err := cv.SelectResources(selectors.Identity(resourceDesc.GetIdentity(compDesc.GetResources())))
	if err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to select resources: %w", err))
	}

	var resourceAccess ocmctx.ResourceAccess
	switch len(resAccesses) {
	case 0:
		return nil, rerror.AsRetryableError(errors.New("no resources selected"))
	case 1:
		resourceAccess = resAccesses[0]
	default:
		return nil, rerror.AsRetryableError(errors.New("cannot determine the resource access unambiguously"))
	}

	return resourceAccess, nil
}

func getBlobAccess(ctx context.Context, access ocmctx.ResourceAccess) (_ blobaccess.BlobAccess, retErr rerror.ReconcileError) {
	log.FromContext(ctx).V(1).Info("get resource blob access")

	// Create data access
	accessMethod, err := access.AccessMethod()
	if err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to create access method: %w", err))
	}

	return accessMethod.AsBlobAccess(), retErr
}

func verifyResource(ctx context.Context, access ocmctx.ResourceAccess, blobAccess blobaccess.BlobAccess, cv ocmctx.ComponentVersionAccess, cd *compdesc.ComponentDescriptor,
) (retErr rerror.ReconcileError) {
	log.FromContext(ctx).V(1).Info("verify resource")

	store := signing.NewLocalVerifiedStore()
	store.Add(cd)

	ok, err := signing.VerifyResourceDigestByResourceAccess(cv, access, blobAccess, store)
	if !ok {
		if err != nil {
			return rerror.AsRetryableError(fmt.Errorf("verification failed: %w", err))
		}

		return rerror.AsRetryableError(errors.New("expected signature verification to be relevant, but it was not"))
	}
	if err != nil {
		return rerror.AsRetryableError(fmt.Errorf("failed to verify resource digest: %w", err))
	}

	return nil
}

func downloadResource(ctx context.Context, octx ocmctx.Context, tmp string, resource *v1alpha1.Resource, acc ocmctx.ResourceAccess, bAcc blobaccess.BlobAccess,
) (
	_ string, retErr rerror.ReconcileError,
) {
	log.FromContext(ctx).V(1).Info("download resource")

	// Using a redirected resource acc to prevent redundant download
	accessMock, err := ocm.NewRedirectedResourceAccess(acc, bAcc)
	if err != nil {
		return "", rerror.AsRetryableError(fmt.Errorf("failed to create redirected resource acc: %w", err))
	}

	path, err := download.DownloadResource(octx, accessMock, filepath.Join(tmp, resource.Name))
	if err != nil {
		return "", rerror.AsRetryableError(fmt.Errorf("failed to download resource: %w", err))
	}

	return path, nil
}

func reconcileArtifact(ctx context.Context, octx ocmctx.Context, storage *storage.Storage, resource *v1alpha1.Resource, acc ocmctx.ResourceAccess, cv ocmctx.ComponentVersionAccess,
	cd *compdesc.ComponentDescriptor, revision string, artifact artifactv1.Artifact,
) (
	retErr rerror.ReconcileError,
) {
	log.FromContext(ctx).V(1).Info("reconcile artifact")

	artifactPresent := strings.Split(filepath.Base(storage.LocalPath(artifact)), ".")[0] == revision

	// Init variables with default values
	dirPath := ""
	archiveFunc := func(_ *artifactv1.Artifact, _ string) error {
		return nil
	}

	// Check if artifact is already present
	if !artifactPresent {
		// bAcc will be closed automatically
		bAcc, rErr := getBlobAccess(ctx, acc)
		if rErr != nil {
			return rErr
		}

		if rErr = verifyResource(ctx, acc, bAcc, cv, cd); rErr != nil {
			return rErr
		}

		// Path in which the resource is downloaded
		tmp, err := os.MkdirTemp("", "resource-*")
		if err != nil {
			return rerror.AsRetryableError(fmt.Errorf("failed to create temporary directory: %w", err))
		}
		// TODO: Discuss if we should cache the downloaded resources
		defer func() {
			retErr = rerror.AsRetryableError(errors.Join(retErr, os.RemoveAll(tmp)))
		}()

		path, rErr := downloadResource(ctx, octx, tmp, resource, acc, bAcc)
		if rErr != nil {
			return rErr
		}

		archiveFunc = func(art *artifactv1.Artifact, _ string) error {
			// If given path is already an archive (e.g. helm charts), just copy it.
			switch extension := filepath.Ext(path); extension {
			case ".tar", ".tar.gz", ".tgz":
				if err := storage.CopyFromPath(art, path); err != nil {
					return fmt.Errorf("failed to copy file: %w", err)
				}
			// Otherwise, archive the directory
			default:
				// Archive directory to storage
				if err := storage.Archive(art, tmp, nil); err != nil {
					return fmt.Errorf("failed to archive: %w", err)
				}
			}

			resource.Status.ArtifactRef = corev1.LocalObjectReference{
				Name: art.Name,
			}

			return nil
		}

		dirPath = tmp
	}

	if err := storage.ReconcileStorage(ctx, resource); err != nil {
		return rerror.AsRetryableError(fmt.Errorf("failed to reconcile resource storage: %w", err))
	}

	// Provide artifact in storage
	// TODO: NewArtifactFor does not sanitize the name. Could break if name too long
	if err := storage.ReconcileArtifact(ctx, resource, revision, dirPath, revision+".tar.gz", archiveFunc); err != nil {
		return rerror.AsRetryableError(fmt.Errorf("failed to reconcile resource artifact: %w", err))
	}

	return retErr
}

func setResourceStatus(ctx context.Context, resource *v1alpha1.Resource, resourceAccess ocmctx.ResourceAccess) error {
	log.FromContext(ctx).V(1).Info("updating resource status")

	// Update status
	accessSpec, err := resourceAccess.Access()
	if err != nil {
		return rerror.AsRetryableError(fmt.Errorf("failed to get access spec: %w", err))
	}

	accessData, err := json.Marshal(accessSpec)
	if err != nil {
		return rerror.AsRetryableError(fmt.Errorf("failed to marshal access spec: %w", err))
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

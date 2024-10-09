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
	"io"
	"os"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/mandelsoft/filepath/pkg/filepath"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"ocm.software/ocm/api/datacontext"
	ocmctx "ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/compdesc"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/ocmutils"
	"ocm.software/ocm/api/ocm/resolvers"
	"ocm.software/ocm/api/ocm/selectors"
	"ocm.software/ocm/api/ocm/tools/signing"
	"ocm.software/ocm/api/utils/blobaccess/blobaccess"
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

func (r *Reconciler) reconcilePrepare(ctx context.Context, resource *v1alpha1.Resource) (_ ctrl.Result, retErr rerror.ReconcileError) {
	logger := log.FromContext(ctx)

	patchHelper := patch.NewSerialPatcher(resource, r.Client)

	// Always attempt to patch the object and status after each reconciliation.
	defer func() {
		if err := status.UpdateStatus(ctx, patchHelper, resource, r.EventRecorder, resource.GetRequeueAfter(), retErr); err != nil {
			retErr = rerror.AsRetryableError(errors.Join(retErr, err))
		}
	}()

	// Get component to resolve resource from component descriptor and verify digest
	component := &v1alpha1.Component{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: resource.Spec.ComponentRef.Namespace,
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

		return ctrl.Result{RequeueAfter: resource.GetRequeueAfter()}, nil
	}

	return r.reconcile(ctx, resource, component)
}

func (r *Reconciler) reconcile(ctx context.Context, resource *v1alpha1.Resource, component *v1alpha1.Component) (_ ctrl.Result, retErr rerror.ReconcileError) {
	// DefaultContext is essentially the same as the extended context created here. The difference is, if we
	// register a new type at an extension point (e.g. a new access type), it's only registered at this exact context
	// instance and not at the global default context variable.
	octx := ocmctx.New(datacontext.MODE_EXTENDED)
	defer func() {
		retErr = rerror.AsRetryableError(errors.Join(retErr, octx.Finalize()))
	}()
	session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

	if retErr = ocm.ConfigureOCMContext(ctx, r, octx, resource, component); retErr != nil {
		return ctrl.Result{}, retErr
	}

	// Get artifact from component that contains component descriptor
	artifactComponent := &artifactv1.Artifact{}
	if err := r.Get(ctx, types.NamespacedName{
		// TODO: Discuss if we should use OwnerReference instead of refs?
		Namespace: resource.Spec.ComponentRef.Namespace,
		Name:      component.Status.ArtifactRef.Name,
	}, artifactComponent); err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetArtifactFailedReason, "Cannot get component artifact")

		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to get component artifact: %w", err))
	}

	// Get component descriptor set
	cdSet, rErr := r.getComponentSetForArtifact(resource, artifactComponent)
	if rErr != nil {
		return ctrl.Result{}, rErr
	}

	// Get referenced component descriptor
	cd, err := cdSet.LookupComponentVersion(component.Status.Component.Component, component.Status.Component.Version)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetComponentDescriptorsFailedReason, err.Error())

		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to lookup component descriptor: %w", err))
	}

	// Get resource and respective component descriptor
	reference := v1.ResourceReference{
		Resource: resource.Spec.Resource.ByReference.Resource,
		// ReferencePath: resource.Spec.Resource.ByReference.ReferencePath,
	}

	resourceArtifact, resourceCd, err := compdesc.ResolveResourceReference(cd, reference, cdSet)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.ResolveResourceFailedReason, err.Error())

		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to resolve resource reference: %w", err))
	}

	cv, rErr := r.getComponentVersion(resource, octx, session, component.Status.Component.RepositorySpec.Raw, resourceCd)
	if rErr != nil {
		return ctrl.Result{}, rErr
	}

	// Get resource access for identity
	resourceAccess, rErr := r.getResourceAccess(resource, cv, resourceArtifact, resourceCd)
	if rErr != nil {
		return ctrl.Result{}, rErr
	}

	rErr = r.verifyAndReconcileResource(ctx, resource, resourceAccess, cv, cd, reference)
	if rErr != nil {
		return ctrl.Result{}, rErr
	}

	// Update status
	accessSpec, err := resourceAccess.Access()
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetResourceAccessDataFailedReason, err.Error())

		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to get access spec: %w", err))
	}

	accessData, err := json.Marshal(accessSpec)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetResourceAccessDataFailedReason, err.Error())

		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to marshal access spec: %w", err))
	}

	resource.Status.Component = component.Status.Component
	resource.Status.Resource = &v1alpha1.ResourceInfo{
		Name:          resourceAccess.Meta().Name,
		Type:          resourceAccess.Meta().Type,
		Version:       resourceAccess.Meta().Version,
		ExtraIdentity: resourceAccess.Meta().ExtraIdentity,
		Access:        apiextensionsv1.JSON{Raw: accessData},
	}
	// TODO: Copy SecretRefs, ConfigRefs, and ConfigSet that "worked"
	status.MarkReady(r.EventRecorder, resource, "Applied version %s", resourceAccess.Meta().Version)

	return ctrl.Result{RequeueAfter: resource.GetRequeueAfter()}, nil
}

func (r *Reconciler) getComponentSetForArtifact(resource *v1alpha1.Resource, artifact *artifactv1.Artifact) (_ *compdesc.ComponentVersionSet, retErr rerror.ReconcileError) {
	tmp, err := os.MkdirTemp("", "component-*")
	if err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to create temporary directory: %w", err))
	}
	defer func() {
		retErr = rerror.AsRetryableError(errors.Join(retErr, os.RemoveAll(tmp)))
	}()

	unlock, err := r.Storage.Lock(*artifact)
	if err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to lock artifact: %w", err))
	}
	defer unlock()

	filePath := filepath.Join(tmp, artifact.Name)

	if err := r.Storage.CopyToPath(artifact, "", filePath); err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to copy artifact to path: %w", err))
	}

	// Read component descriptor list
	file, err := os.Open(filepath.Join(filePath, v1alpha1.OCMComponentDescriptorList))
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.ReadFileFailedReason, err.Error())

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
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.UnmarshallingComponentDescriptorsFailedReason, err.Error())

		return nil, rerror.AsRetryableError(fmt.Errorf("failed to unmarshal component descriptors: %w", err))
	}

	return compdesc.NewComponentVersionSet(cds.List...), nil
}

func (r *Reconciler) getComponentVersion(resource *v1alpha1.Resource, octx ocmctx.Context, session ocmctx.Session, spec []byte, compDesc *compdesc.ComponentDescriptor) (
	ocmctx.ComponentVersionAccess, rerror.ReconcileError,
) {
	// Get repository and resolver to get the respective component version of the resource
	repoSpec, err := octx.RepositorySpecForConfig(spec, nil)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.RepositorySpecInvalidReason, err.Error())

		return nil, rerror.AsRetryableError(fmt.Errorf("failed to get repository spec: %w", err))
	}
	repo, err := session.LookupRepository(octx, repoSpec)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetRepositoryFailedReason, err.Error())

		return nil, rerror.AsRetryableError(fmt.Errorf("failed to lookup repository: %w", err))
	}

	resolver := resolvers.NewCompoundResolver(repo, octx.GetResolver())

	// Get component version for resource access
	cv, err := session.LookupComponentVersion(resolver, compDesc.Name, compDesc.Version)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetComponentVersionFailedReason, err.Error())

		return nil, rerror.AsRetryableError(fmt.Errorf("failed to lookup component version: %w", err))
	}

	return cv, nil
}

func (r *Reconciler) getResourceAccess(resource *v1alpha1.Resource, cv ocmctx.ComponentVersionAccess, resourceDesc *compdesc.Resource, compDesc *compdesc.ComponentDescriptor,
) (ocmctx.ResourceAccess, rerror.ReconcileError) {
	resAccesses, err := cv.SelectResources(selectors.Identity(resourceDesc.GetIdentity(compDesc.GetResources())))
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetResourceAccessFailedReason, err.Error())

		return nil, rerror.AsRetryableError(fmt.Errorf("failed to select resources: %w", err))
	}

	var resourceAccess ocmctx.ResourceAccess
	switch len(resAccesses) {
	case 0:
		err := errors.New("no resources selected")
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetResourceAccessFailedReason, err.Error())

		return nil, rerror.AsRetryableError(err)
	case 1:
		resourceAccess = resAccesses[0]
	default:
		err := errors.New("cannot determine the resource unambiguously")
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetResourceAccessFailedReason, err.Error())

		return nil, rerror.AsRetryableError(err)
	}

	return resourceAccess, nil
}

func (r *Reconciler) verifyAndReconcileResource(ctx context.Context, resource *v1alpha1.Resource, access ocmctx.ResourceAccess, cv ocmctx.ComponentVersionAccess, cd *compdesc.ComponentDescriptor,
	reference v1.ResourceReference,
) (
	retErr rerror.ReconcileError,
) {
	// Write resource
	tmpDir, err := os.MkdirTemp("", "resource-*")
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.TemporaryFolderCreationFailedReason, err.Error())

		return rerror.AsRetryableError(fmt.Errorf("failed to create temporary directory: %w", err))
	}
	// TODO: Discuss if we should cache the downloaded resources
	defer func() {
		retErr = rerror.AsRetryableError(errors.Join(retErr, os.RemoveAll(tmpDir)))
	}()

	// Get resource reader
	reader, err := ocmutils.GetResourceReader(access)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.GetResourceReaderFailedReason, err.Error())

		return rerror.AsRetryableError(fmt.Errorf("failed to create reader: %w", err))
	}
	defer func() {
		retErr = rerror.AsRetryableError(errors.Join(retErr, reader.Close()))
	}()

	// Only required for digest calculation and to provide through the artifact server
	const perm = 0o600
	pathFile := filepath.Join(tmpDir, resource.Name)
	file, err := os.OpenFile(pathFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.OpenFileFailedReason, err.Error())

		return rerror.AsRetryableError(fmt.Errorf("failed to open resource file: %w", err))
	}
	defer func() {
		retErr = rerror.AsRetryableError(errors.Join(retErr, file.Close()))
	}()

	// Copy content to file
	// TODO: Discuss if we need a size limitation for copying?
	_, err = io.Copy(file, reader)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.CopyFileFailedReason, err.Error())

		return rerror.AsRetryableError(fmt.Errorf("failed to copy resource file: %w", err))
	}

	revision := string(reference.Resource.Digest())

	// Verify a resource by calculating digest and comparing it with the one in the component version.
	dataAccess := blobaccess.DataAccessForReaderFunction(func() (io.ReadCloser, error) {
		return os.OpenFile(pathFile, os.O_RDONLY, 0)
	}, pathFile)

	store := signing.NewLocalVerifiedStore()
	store.Add(cd)

	ok, err := signing.VerifyResourceDigestByResourceAccess(cv, access, dataAccess, store)
	if !ok {
		if err != nil {
			status.MarkNotReady(r.EventRecorder, resource, v1alpha1.DigestVerificationFailedReason, err.Error())

			return rerror.AsRetryableError(fmt.Errorf("verification failed: %w", err))
		}
		err = errors.New("expected signature verification to be relevant, but it was not")
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.DigestVerificationFailedReason, err.Error())

		return rerror.AsRetryableError(err)
	}
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.DigestVerificationFailedReason, err.Error())

		return rerror.AsRetryableError(fmt.Errorf("failed to verify resource digest: %w", err))
	}

	err = r.Storage.ReconcileStorage(ctx, resource)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.StorageReconcileFailedReason, err.Error())

		return rerror.AsRetryableError(fmt.Errorf("failed to reconcile resource storage: %w", err))
	}

	// Provide artifact in storage
	// TODO: NewArtifactFor does not sanitize the name. Could break if name too long
	if err := r.Storage.ReconcileArtifact(ctx, resource, revision, tmpDir, revision+".tar.gz",
		func(art *artifactv1.Artifact, _ string) error {
			// Archive directory to storage
			if err := r.Storage.Archive(art, tmpDir, nil); err != nil {
				return fmt.Errorf("failed to reconcile resource artifact: %w", err)
			}

			resource.Status.ArtifactRef = corev1.LocalObjectReference{
				Name: art.Name,
			}

			return nil
		},
	); err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.ReconcileArtifactFailedReason, err.Error())

		return rerror.AsRetryableError(fmt.Errorf("failed to reconcile resource artifact: %w", err))
	}

	return nil
}

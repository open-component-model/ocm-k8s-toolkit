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
	"net/http"
	"os"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/fluxcd/pkg/tar"
	"github.com/mandelsoft/filepath/pkg/filepath"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
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
	"sigs.k8s.io/yaml"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/rerror"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

// ResourceReconciler reconciles a Resource object.
type Reconciler struct {
	*ocm.BaseReconciler
	Storage *storage.Storage
}

var _ ocm.Reconciler = (*Reconciler)(nil)

// SetupWithManager sets up the controller with the Manager.
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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
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
		if pErr := status.UpdateStatus(ctx, patchHelper, resource, r.EventRecorder, resource.GetRequeueAfter(), retErr); pErr != nil {
			retErr = rerror.AsRetryableError(errors.Join(retErr, pErr))
		}
	}()

	// TODO: Check if the repository is also needed

	// Get component for verification and resolving
	component := &v1alpha1.Component{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: resource.Spec.ComponentRef.Namespace,
		Name:      resource.Spec.ComponentRef.Name,
	}, component); err != nil {
		logger.Info("failed to get component")

		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to get component: %w", err))
	}

	if !conditions.IsReady(component) {
		logger.Info("component is not ready", "name", resource.Spec.ComponentRef.Name)
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.ComponentIsNotReadyReason, "Component is not ready")

		return ctrl.Result{Requeue: true}, nil
	}

	return r.reconcile(ctx, resource, component)
}

func (r *Reconciler) reconcile(ctx context.Context, resource *v1alpha1.Resource, component *v1alpha1.Component) (_ ctrl.Result, retErr rerror.ReconcileError) {
	// TODO: I need the repository Reference because the component-descriptor does not contain the information

	logger := log.FromContext(ctx)
	var err error
	var rerr rerror.ReconcileError
	// DefaultContext is essentially the same as the extended context created here. The difference is, if we
	// register a new type at an extension point (e.g. a new access type), it's only registered at this exact context
	// instance and not at the global default context variable.
	octx := ocmctx.New(datacontext.MODE_EXTENDED)
	defer func() {
		err = octx.Finalize()
		if err != nil {
			retErr = rerror.AsNonRetryableError(errors.Join(retErr, err))
		}
	}()
	session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

	rerr = ocm.ConfigureOCMContext(ctx, r, octx, resource, component)
	if err != nil {
		return ctrl.Result{}, rerr
	}

	// Get Artifact from component
	artifactComponent := &artifactv1.Artifact{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: resource.Spec.ComponentRef.Namespace,
		Name:      component.Status.ArtifactRef.Name,
	}, artifactComponent); err != nil {
		logger.Info("failed to get component artifact")

		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to get component artifact: %w", err))
	}

	// Get artifact content (Component Descriptor)
	resp, err := http.Get(artifactComponent.Spec.URL)
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to get component artifact response: %w", err))
	}

	// Create temporary directory to untar component descriptor
	tmpComponent, err := os.MkdirTemp("/tmp", "component-descriptor-")
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to create temporary directory: %w", err))
	}
	defer func() {
		if err = os.RemoveAll(tmpComponent); err != nil {
			retErr = rerror.AsNonRetryableError(errors.Join(retErr, err))
		}
	}()

	if err := tar.Untar(resp.Body, tmpComponent); err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to untar component artifact: %w", err))
	}

	// Read component descriptor list
	cdByte, err := os.ReadFile(filepath.Join(tmpComponent, v1alpha1.OCMComponentDescriptorList))
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to read component descriptor: %w", err))
	}

	// Get component descriptor set
	cds := &ocm.Descriptors{}
	if err := yaml.Unmarshal(cdByte, cds); err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to unmarshal component descriptor: %w", err))
	}
	cdSet := compdesc.NewComponentVersionSet(cds.List...)

	// Get referenced component descriptor
	cd, err := cdSet.LookupComponentVersion(component.Status.Component.Component, component.Status.Component.Version)
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to lookup component descriptor: %w", err))
	}

	resRef := v1.ResourceReference{
		Resource:      resource.Spec.Resource.ByReference.Resource,
		ReferencePath: resource.Spec.Resource.ByReference.ReferencePath,
	}

	// Get resource and respective component descriptor
	resourceArtifact, cdResource, err := compdesc.ResolveResourceReference(cd, resRef, cdSet)
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to resolve resource reference: %w", err))
	}

	// Get repository and resolver to get the respective component version of the resource
	repoSpec, err := octx.RepositorySpecForConfig(component.Status.Component.RepositorySpec.Raw, nil)
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to get repository spec: %w", err))
	}
	repo, err := session.LookupRepository(octx, repoSpec)
	if err != nil {
		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to lookup repository: %w", err))
	}

	resolver := resolvers.NewCompoundResolver(repo, octx.GetResolver())

	// Get component version for resource
	cv, err := session.LookupComponentVersion(resolver, cdResource.Name, cdResource.Version)
	if err != nil {
		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to lookup component version: %w", err))
	}

	// Get resource accesses for identity
	resAccesses, err := cv.SelectResources(selectors.Identity(resourceArtifact.GetIdentity(cdResource.GetResources())))
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to select resources: %w", err))
	}
	if len(resAccesses) == 0 {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("no resources selected"))
	}
	// TODO: Check if the selector should identify only one resource
	if len(resAccesses) < 1 {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("cannot determine the resource unambiguously"))
	}

	// TODO: This does not make sense because the identity is the same as for the resource accesses
	//  But we need the index later to verify the digest
	index := cv.GetResourceIndex(resourceArtifact.GetIdentity(cdResource.GetResources()))
	if index < 0 {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("cannot determine the index unambiguously"))
	}
	resAccess := resAccesses[index]

	// Get resource reader
	reader, err := ocmutils.GetResourceReader(resAccess)
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to create reader: %w", err))
	}
	defer func() {
		if err = reader.Close(); err != nil {
			retErr = rerror.AsNonRetryableError(errors.Join(retErr, err))
		}
	}()

	// Create temporary directory to write resource
	tmpDirResource, err := os.MkdirTemp("/tmp", "resource-")
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to create temporary directory: %w", err))
	}
	defer func() {
		if err := os.RemoveAll(tmpDirResource); err != nil {
			retErr = rerror.AsNonRetryableError(errors.Join(retErr, err))
		}
	}()

	// TODO: Is this safe or do we have to normalize?
	revision := resAccess.Meta().Name + "-" + resAccess.Meta().Version

	file, err := os.OpenFile(filepath.Join(tmpDirResource, revision), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o766)
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to open resource file: %w", err))
	}
	defer func() {
		if err := file.Close(); err != nil {
			retErr = rerror.AsNonRetryableError(errors.Join(retErr, err))
		}
	}()

	// Copy content to file
	_, err = io.Copy(file, reader)
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to copy resource file: %w", err))
	}

	// Calculate digest
	// Ensure the directory exists and acquire lock
	artifactResource := r.Storage.NewArtifactFor(resource.GetKind(), resource.GetObjectMeta(), revision, file.Name())
	if err := r.Storage.MkdirAll(artifactResource); err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to archive artifactComponent: %w", err))
	}
	if err := r.Storage.Archive(&artifactResource, tmpDirResource, nil); err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to archive artifactComponent: %w", err))
	}

	dataBytes, err := os.ReadFile(file.Name())
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to read resource file: %w", err))
	}
	dataAccess := blobaccess.DataAccessForData(dataBytes)

	ok, err := signing.VerifyResourceDigest(cv, index, dataAccess)
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to verify resource digest: %w", err))
	}
	if !ok {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("verification failed"))
	}

	err = r.Storage.ReconcileStorage(ctx, resource)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.StorageReconcileFailedReason, err.Error())

		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to reconcile resource storage: %w", err))
	}

	// Provide artifact in storage
	if err := r.Storage.ReconcileArtifact(ctx, resource, revision, tmpDirResource, revision+".tar.gz",
		func(art *artifactv1.Artifact, _ string) error {
			// Archive directory to storage
			if err := r.Storage.Archive(art, tmpDirResource, nil); err != nil {
				return fmt.Errorf("unable to archive artifactComponent to storage: %w", err)
			}

			resource.Status.ArtifactRef = corev1.LocalObjectReference{
				Name: art.Name,
			}

			return nil
		},
	); err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to reconcile artifactComponent: %w", err))
	}

	// Update status
	accessSpec, err := resAccess.Access()
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to get access spec: %w", err))
	}

	accessData, err := json.Marshal(accessSpec)
	if err != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(fmt.Errorf("failed to marshal access spec: %w", err))
	}

	resource.Status.Component = component.Status.Component
	resource.Status.Resource = &v1alpha1.ResourceInfo{
		Name:          resAccess.Meta().Name,
		Type:          resAccess.Meta().Type,
		Version:       resAccess.Meta().Version,
		ExtraIdentity: resAccess.Meta().ExtraIdentity,
		Access:        apiextensionsv1.JSON{Raw: accessData},
	}
	// TODO: Copy SecretRefs, ConfigRefs, and ConfigSet that "worked"
	status.MarkReady(r.EventRecorder, resource, "Applied version %s", resAccess.Meta().Version)

	return ctrl.Result{RequeueAfter: resource.GetRequeueAfter()}, nil
}

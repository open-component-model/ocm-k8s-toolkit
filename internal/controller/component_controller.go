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

package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/ocm"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// ComponentReconciler reconciles a Component object
type ComponentReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Storage   *storage.Storage
	OCMClient ocm.Client
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=components,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=components/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=components/finalizers,verbs=update

// +kubebuilder:rbac:groups=openfluxcd.mandelsoft.org,resources=artifacts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openfluxcd.mandelsoft.org,resources=artifacts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openfluxcd.mandelsoft.org,resources=artifacts/finalizers,verbs=update

// Reconcile the component object.
func (r *ComponentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	logger := log.FromContext(ctx).WithName("component-controller")

	obj := &deliveryv1alpha1.Component{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if obj.GetDeletionTimestamp() != nil {
		logger.Info("deleting component", "name", obj.Name)
		return ctrl.Result{}, nil
	}

	if obj.Spec.Suspend {
		logger.Info("component is suspended, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	patchHelper := patch.NewSerialPatcher(obj, r.Client)

	// Always attempt to patch the object and status after each reconciliation.
	defer func() {
		if perr := patchHelper.Patch(ctx, obj); perr != nil {
			retErr = errors.Join(retErr, perr)
		}
	}()

	// TODO: Add defer patch object status and conditions

	repositoryObject := &deliveryv1alpha1.OCMRepository{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: obj.Spec.RepositoryRef.Namespace,
		Name:      obj.Spec.RepositoryRef.Name,
	}, repositoryObject); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get repository: %w", err)
	}

	// Reconcile the storage to create the main location and prepare the server.
	if err := r.Storage.ReconcileStorage(ctx, obj); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile storage: %w", err)
	}

	// Create temp working dir
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("%s-%s-%s-", obj.Kind, obj.Namespace, obj.Name))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create temporary working directory: %w", err)
	}
	defer func() {
		if err = os.RemoveAll(tmpDir); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to remove temporary working directory")
		}
	}()

	// Get the descriptor
	octx, err := r.OCMClient.CreateAuthenticatedOCMContext(ctx, repositoryObject)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create authenticated OCM context: %w", err)
	}

	cv, err := r.OCMClient.GetComponentVersion(ctx, octx, obj, repositoryObject)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to retrieve component: %w", err)
	}

	desc := cv.GetDescriptor()

	// TODO: This needs to be a list and recursively fetch component descriptors for references.
	content, err := yaml.Marshal(desc)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to marshal content: %w", err)
	}

	if err := os.WriteFile("component-descriptor.yaml", content, 0o755); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to write file: %w", err)
	}

	// componentversionname-componentversionversion.tar.gz
	// It could be possible that this is not enough.
	revision := r.normalizeComponentVersionName(cv.GetName()) + "-" + cv.GetVersion()
	// sha of this file as a revision?
	if err := r.Storage.ReconcileArtifact(ctx, obj, revision, tmpDir, revision+".tar.gz", func(art *artifactv1.Artifact, s string) error {
		// Archive directory to storage
		if err := r.Storage.Archive(art, tmpDir, nil); err != nil {
			return fmt.Errorf("unable to archive artifact to storage: %w", err)
		}

		obj.Status.ArtifactName = art.Name

		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile artifact: %w", err)
	}

	// Update status

	// Return done.

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Component{}).
		Complete(r)
}

func (r *ComponentReconciler) normalizeComponentVersionName(name string) string {
	return strings.ReplaceAll(name, "/", "-")
}

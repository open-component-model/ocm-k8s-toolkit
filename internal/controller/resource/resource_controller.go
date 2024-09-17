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
	"errors"
	"fmt"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/openfluxcd/controller-manager/storage"
	"k8s.io/apimachinery/pkg/types"
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

// Reconciler reconciles a Resource object.
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
		Namespace: component.Spec.RepositoryRef.Namespace,
		Name:      component.Spec.RepositoryRef.Name,
	}, component); err != nil {
		logger.Info("failed to get component")

		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to get repository: %w", err))
	}

	if !conditions.IsReady(component) {
		logger.Info("component is not ready", "name", resource.Spec.ComponentRef.Name)
		status.MarkNotReady(r.EventRecorder, resource, v1alpha1.ComponentIsNotReadyReason, "Component is not ready")

		return ctrl.Result{Requeue: true}, nil
	}

	return r.reconcile(ctx, resource, component)
}

func (r *Reconciler) reconcile(ctx context.Context, resource *v1alpha1.Resource, component *v1alpha1.Component) (_ ctrl.Result, retErr rerror.ReconcileError) {

	// TODO: Configure/Create context and session

	// TODO: Download resource
	//         Get comp-list from comp-controller => NewComponentVersionSet
	//         Resolve ComponentDescriptor => Resources, ComponentDescriptor

	// TODO: Verify resource
	//         Calculate digest for retrieved resource and compare it with digest retrieved from ComponentDescriptor (which is verified in the comp-controller)

	// TODO: Provide resource
	//         Create storage (openfluxcd/controller-manager) or use storage.
	//           Discuss if we need several storages or only one (note: everything runs in one pod)
	//         Create artifact from resource and provide it through the storage-server

	return ctrl.Result{RequeueAfter: resource.GetRequeueAfter()}, nil
}

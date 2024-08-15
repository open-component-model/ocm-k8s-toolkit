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

	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/status"
	"k8s.io/apimachinery/pkg/runtime"
	kuberecorder "k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// OCMRepositoryReconciler reconciles a OCMRepository object.
type OCMRepositoryReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	kuberecorder.EventRecorder
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmrepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmrepositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmrepositories/finalizers,verbs=update

func (r *OCMRepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	_ = log.FromContext(ctx)

	obj := &deliveryv1alpha1.OCMRepository{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper := patch.NewSerialPatcher(obj, r.Client)

	// Always attempt to patch the object and status after each reconciliation.
	defer func() {
		if perr := status.UpdateStatus(ctx, patchHelper, obj, r.EventRecorder, obj.GetRequeueAfter(), retErr); perr != nil {
			retErr = errors.Join(retErr, perr)
		}
	}()

	status.MarkReady(r.EventRecorder, obj, "Successfully reconciled")

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *OCMRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.OCMRepository{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

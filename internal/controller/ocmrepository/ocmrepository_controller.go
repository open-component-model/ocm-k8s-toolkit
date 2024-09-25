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

package ocmrepository

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxcd/pkg/runtime/patch"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	kuberecorder "k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

const (
	repositoryFinalizer = "finalizers.ocm.software"
	repositoryKey       = ".metadata.repository"
)

// Reconciler reconciles a OCMRepository object.
type Reconciler struct {
	client.Client
	Scheme *runtime.Scheme
	kuberecorder.EventRecorder
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmrepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmrepositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmrepositories/finalizers,verbs=update

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	_ = log.FromContext(ctx)

	obj := &v1alpha1.OCMRepository{}
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

	if obj.GetDeletionTimestamp() != nil {
		if !controllerutil.ContainsFinalizer(obj, repositoryFinalizer) {
			return ctrl.Result{}, nil
		}

		if err := r.reconcileDeleteRepository(ctx, obj); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
		}

		return ctrl.Result{}, nil
	}

	// AddFinalizer is not present already.
	controllerutil.AddFinalizer(obj, repositoryFinalizer)

	status.MarkReady(r.EventRecorder, obj, "Successfully reconciled")

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.Component{}, repositoryKey, func(rawObj client.Object) []string {
		comp, ok := rawObj.(*v1alpha1.Component)
		if !ok {
			return nil
		}

		return []string{fmt.Sprintf("%s/%s", comp.Spec.RepositoryRef.Namespace, comp.Spec.RepositoryRef.Name)}
	}); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.OCMRepository{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

func (r *Reconciler) reconcileDeleteRepository(ctx context.Context, obj *v1alpha1.OCMRepository) error {
	logger := log.FromContext(ctx)
	componentList := &v1alpha1.ComponentList{}
	if err := r.List(ctx, componentList, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector(repositoryKey, client.ObjectKeyFromObject(obj).String()),
	}); err != nil {
		return fmt.Errorf("failed to list components: %w", err)
	}

	if len(componentList.Items) == 0 {
		controllerutil.RemoveFinalizer(obj, repositoryFinalizer)
	} else {
		logger.Info("number of components are referencing this repository, deletion aborted", "number", len(componentList.Items))
	}

	return nil
}

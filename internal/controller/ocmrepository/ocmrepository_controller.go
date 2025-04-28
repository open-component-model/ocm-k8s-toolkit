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
	"fmt"
	"strings"

	"github.com/fluxcd/pkg/runtime/patch"
	"k8s.io/apimachinery/pkg/fields"
	"ocm.software/ocm/api/datacontext"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	ocmctx "ocm.software/ocm/api/ocm"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

var repositoryKey = ".spec.repositoryRef"

// OCMRepositoryReconciler reconciles a OCMRepository object.
type Reconciler struct {
	*ocm.BaseReconciler
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmrepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmrepositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmrepositories/finalizers,verbs=update

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("starting reconciliation")

	ocmRepo := &v1alpha1.OCMRepository{}
	if err := r.Get(ctx, req.NamespacedName, ocmRepo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper := patch.NewSerialPatcher(ocmRepo, r.Client)
	defer func(ctx context.Context) {
		err = status.UpdateStatus(ctx, patchHelper, ocmRepo, r.EventRecorder, ocmRepo.GetRequeueAfter(), err)
	}(ctx)

	if !ocmRepo.GetDeletionTimestamp().IsZero() {
		if !controllerutil.ContainsFinalizer(ocmRepo, v1alpha1.OCMRepositoryFinalizer) {
			return ctrl.Result{}, nil
		}

		return r.reconcileDeleteRepository(ctx, ocmRepo)
	}

	// AddFinalizer if not present already.
	if added := controllerutil.AddFinalizer(ocmRepo, v1alpha1.OCMRepositoryFinalizer); added {
		err := r.Update(ctx, ocmRepo)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}

		return ctrl.Result{Requeue: true}, nil
	}

	if ocmRepo.Spec.Suspend {
		logger.Info("OCMRepository is suspended, skipping reconciliation")

		return ctrl.Result{}, nil
	}

	logger.Info("reconciling OCM repository")
	octx := ocmctx.New(datacontext.MODE_EXTENDED)
	defer func() {
		err = octx.Finalize()
	}()

	session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

	configs, err := ocm.GetEffectiveConfig(ctx, r.GetClient(), ocmRepo)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), ocmRepo, v1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get effective config: %w", err)
	}
	err = ocm.ConfigureContext(ctx, octx, r.GetClient(), configs)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), ocmRepo, v1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to configure context: %w", err)
	}

	err = r.validate(octx, session, ocmRepo)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, ocmRepo, v1alpha1.GetOCMRepositoryFailedReason, "failed to validate ocm repository")

		return ctrl.Result{}, fmt.Errorf("failed to validate ocm repository: %w", err)
	}

	r.fillRepoStatusFromSpec(ocmRepo, configs)

	status.MarkReady(r.EventRecorder, ocmRepo, "Successfully reconciled")

	return ctrl.Result{RequeueAfter: ocmRepo.GetRequeueAfter()}, nil
}

func (r *Reconciler) validate(octx ocmctx.Context, session ocmctx.Session, ocmRepo *v1alpha1.OCMRepository) error {
	spec, err := octx.RepositorySpecForConfig(ocmRepo.Spec.RepositorySpec.Raw, nil)
	if err != nil {
		return fmt.Errorf("cannot create RepositorySpec from raw data: %w", err)
	}

	if err = spec.Validate(octx, nil); err != nil {
		return fmt.Errorf("invalid RepositorySpec: %w", err)
	}

	_, err = session.LookupRepository(octx, spec)
	if err != nil {
		return fmt.Errorf("cannot lookup repository for RepositorySpec: %w", err)
	}

	return nil
}

func (r *Reconciler) fillRepoStatusFromSpec(ocmRepo *v1alpha1.OCMRepository,
	configs []v1alpha1.OCMConfiguration,
) {
	ocmRepo.Status.EffectiveOCMConfig = configs
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.Component{}, repositoryKey, func(rawObj client.Object) []string {
		comp, ok := rawObj.(*v1alpha1.Component)
		if !ok {
			return nil
		}

		return []string{fmt.Sprintf("%s/%s", comp.GetNamespace(), comp.Spec.RepositoryRef.Name)}
	}); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.OCMRepository{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

func (r *Reconciler) reconcileDeleteRepository(ctx context.Context, obj *v1alpha1.OCMRepository) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	componentList := &v1alpha1.ComponentList{}
	if err := r.List(ctx, componentList, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector(repositoryKey, client.ObjectKeyFromObject(obj).String()),
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list components: %w", err)
	}

	if len(componentList.Items) > 0 {
		var names []string
		for _, comp := range componentList.Items {
			names = append(names, fmt.Sprintf("%s/%s", comp.Namespace, comp.Name))
		}

		logger.Info("repository is being deleted, please remove the following components referencing it", "names", names)

		return ctrl.Result{}, fmt.Errorf("failed to remove repository referencing components: %s", strings.Join(names, ","))
	}

	if updated := controllerutil.RemoveFinalizer(obj, v1alpha1.OCMRepositoryFinalizer); updated {
		if err := r.Update(ctx, obj); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
		}

		return ctrl.Result{}, nil
	}

	logger.Info("ocm repository is being deleted and still has existing finalizers", "name", obj.GetName())

	return ctrl.Result{Requeue: true}, nil
}

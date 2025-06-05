package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fluxcd/pkg/runtime/patch"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"ocm.software/ocm/api/datacontext"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ocmctx "ocm.software/ocm/api/ocm"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/status"
)

var repositoryKey = ".spec.repositoryRef"

// Reconciler reconciles a Repository object.
type Reconciler struct {
	*ocm.BaseReconciler
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// This index is required to get all components that reference an OCM repository. This is required to make sure that
	// when deleting the OCM repository, no component exists anymore that references that OCM repository.
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.Component{}, repositoryKey, func(rawObj client.Object) []string {
		comp, ok := rawObj.(*v1alpha1.Component)
		if !ok {
			return nil
		}

		return []string{comp.Spec.RepositoryRef.Name}
	}); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Repository{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			// Ensure to reconcile the OCM repository when an component changes that references this OCM repository.
			// We want to reconcile because the OCM repository-finalizer makes sure that the OCM repository is only
			// deleted when it is not referenced by any component anymore. So, when the OCM repository is already marked
			// for deletion, we want to get notified about component changes (e.g. deletion) to remove the OCM
			// repository-finalizer respectively.
			&v1alpha1.Component{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				component, ok := obj.(*v1alpha1.Component)
				if !ok {
					return []reconcile.Request{}
				}

				repo := &v1alpha1.Repository{}
				if err := r.Get(ctx, client.ObjectKey{
					Namespace: component.GetNamespace(),
					Name:      component.Spec.RepositoryRef.Name,
				}, repo); err != nil {
					return []reconcile.Request{}
				}

				// Only reconcile if the OCM repository is marked for deletion
				if repo.GetDeletionTimestamp().IsZero() {
					return []reconcile.Request{}
				}

				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{
						Namespace: repo.GetNamespace(),
						Name:      repo.GetName(),
					}},
				}
			})).
		Complete(r)
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=repositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=repositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=repositories/finalizers,verbs=update

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("starting reconciliation")

	ocmRepo := &v1alpha1.Repository{}
	if err := r.Get(ctx, req.NamespacedName, ocmRepo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper := patch.NewSerialPatcher(ocmRepo, r.Client)
	defer func(ctx context.Context) {
		err = errors.Join(err, status.UpdateStatus(ctx, patchHelper, ocmRepo, r.EventRecorder, ocmRepo.GetRequeueAfter(), err))
	}(ctx)

	if !ocmRepo.GetDeletionTimestamp().IsZero() {
		if !controllerutil.ContainsFinalizer(ocmRepo, v1alpha1.RepositoryFinalizer) {
			return ctrl.Result{}, nil
		}

		if err := r.deleteRepository(ctx, ocmRepo); err != nil {
			status.MarkNotReady(r.EventRecorder, ocmRepo, v1alpha1.DeletionFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to delete OCM repository: %w", err)
		}

		return ctrl.Result{}, nil
	}

	// AddFinalizer if not present already.
	if added := controllerutil.AddFinalizer(ocmRepo, v1alpha1.RepositoryFinalizer); added {
		err := r.Update(ctx, ocmRepo)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}

		return ctrl.Result{Requeue: true}, nil
	}

	if ocmRepo.Spec.Suspend {
		logger.Info("Repository is suspended, skipping reconciliation")

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
		status.MarkNotReady(r.EventRecorder, ocmRepo, v1alpha1.GetRepositoryFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to validate ocm repository: %w", err)
	}

	r.fillRepoStatusFromSpec(ocmRepo, configs)

	status.MarkReady(r.EventRecorder, ocmRepo, "Successfully reconciled")

	return ctrl.Result{RequeueAfter: ocmRepo.GetRequeueAfter()}, nil
}

func (r *Reconciler) validate(octx ocmctx.Context, session ocmctx.Session, ocmRepo *v1alpha1.Repository) error {
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

func (r *Reconciler) fillRepoStatusFromSpec(ocmRepo *v1alpha1.Repository,
	configs []v1alpha1.OCMConfiguration,
) {
	ocmRepo.Status.EffectiveOCMConfig = configs
}

func (r *Reconciler) deleteRepository(ctx context.Context, obj *v1alpha1.Repository) error {
	componentList := &v1alpha1.ComponentList{}
	if err := r.List(ctx, componentList, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector(repositoryKey, client.ObjectKeyFromObject(obj).Name),
	}); err != nil {
		status.MarkNotReady(r.EventRecorder, obj, v1alpha1.DeletionFailedReason, err.Error())

		return fmt.Errorf("failed to list components: %w", err)
	}

	if len(componentList.Items) > 0 {
		var names []string
		for _, comp := range componentList.Items {
			names = append(names, fmt.Sprintf("%s/%s", comp.Namespace, comp.Name))
		}

		msg := fmt.Sprintf(
			"OCM repository cannot be removed as components are still referencing it: %s",
			strings.Join(names, ","),
		)
		status.MarkNotReady(r.EventRecorder, obj, v1alpha1.DeletionFailedReason, msg)

		return errors.New(msg)
	}

	if updated := controllerutil.RemoveFinalizer(obj, v1alpha1.RepositoryFinalizer); updated {
		if err := r.Update(ctx, obj); err != nil {
			status.MarkNotReady(r.EventRecorder, obj, v1alpha1.DeletionFailedReason, err.Error())

			return fmt.Errorf("failed to remove finalizer: %w", err)
		}

		return nil
	}

	status.MarkNotReady(
		r.EventRecorder,
		obj,
		v1alpha1.DeletionFailedReason,
		"OCM repository is being deleted and still has existing finalizers",
	)

	return nil
}

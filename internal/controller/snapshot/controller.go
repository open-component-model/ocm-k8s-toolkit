package snapshot

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	ctrl "sigs.k8s.io/controller-runtime"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	snapshotRegistry "github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
)

// Reconciler reconciles a Snapshot object.
type Reconciler struct {
	*ocm.BaseReconciler
	Registry snapshotRegistry.RegistryType
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=snapshots,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=snapshots/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=snapshots/finalizers,verbs=update

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Watch for snapshot resources
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Snapshot{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// Reconcile add a finalizer on creation to the snapshot resource and handles the deletion of the snapshot by deleting
// the manifest of the OCI artifact in the OCI registry (The OCI registry GC deletes the blobs if no manifest is
// pointing to it).
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Snapshot")

	snapshotResource := &deliveryv1alpha1.Snapshot{}
	if err := r.Get(ctx, req.NamespacedName, snapshotResource); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if snapshotResource.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	if !snapshotResource.GetDeletionTimestamp().IsZero() {
		logger.Info("Deleting snapshot")

		repository, err := r.Registry.NewRepository(ctx, snapshotResource.Spec.Repository)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create a repository: %w", err)
		}

		if err := repository.DeleteSnapshot(ctx, snapshotResource.GetDigest()); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete snapshot: %w", err)
		}

		if removed := controllerutil.RemoveFinalizer(snapshotResource, deliveryv1alpha1.SnapshotFinalizer); removed {
			if err := r.Update(ctx, snapshotResource); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}

		return ctrl.Result{}, nil
	}

	if added := controllerutil.AddFinalizer(snapshotResource, deliveryv1alpha1.SnapshotFinalizer); added {
		err := r.Update(ctx, snapshotResource)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}

		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

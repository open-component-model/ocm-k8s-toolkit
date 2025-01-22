package snapshot

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/log"

	ctrl "sigs.k8s.io/controller-runtime"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
)

// Reconciler reconciles a Snapshot object.
type Reconciler struct {
	*ocm.BaseReconciler
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=snapshots,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=snapshots/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=snapshots/finalizers,verbs=update

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Snapshot{}).
		Named("snapshot").
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	return ctrl.Result{}, nil
}

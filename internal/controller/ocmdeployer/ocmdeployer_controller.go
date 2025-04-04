package ocmdeployer

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxcd/pkg/runtime/patch"
	"k8s.io/apimachinery/pkg/types"
	"ocm.software/ocm/api/datacontext"
	"ocm.software/ocm/api/ocm/compdesc"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	krov1alpha1 "github.com/kro-run/kro/api/v1alpha1"
	ocmctx "ocm.software/ocm/api/ocm"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/util"
)

// Reconciler reconciles a OCMDeployer object.
type Reconciler struct {
	*ocm.BaseReconciler
}

var _ ocm.Reconciler = (*Reconciler)(nil)

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmdeployers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmdeployers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmdeployers/finalizers,verbs=update
// +kubebuilder:rbac:groups=kro.run,resources=resourcegraphdefinitions,verbs=list;watch;create;update;patch

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Create index for resource reference name from deployers
	const fieldName = "spec.resourceRef.name"
	if err := mgr.GetFieldIndexer().IndexField(ctx, &deliveryv1alpha1.OCMDeployer{}, fieldName, func(obj client.Object) []string {
		deployer, ok := obj.(*deliveryv1alpha1.OCMDeployer)
		if !ok {
			return nil
		}

		return []string{deployer.Spec.ResourceRef.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.OCMDeployer{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// Watch for resource-events that are referenced by the deployer
		Watches(
			&deliveryv1alpha1.Resource{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				resource, ok := obj.(*deliveryv1alpha1.Resource)
				if !ok {
					return []reconcile.Request{}
				}

				// Get list of deployers that reference the resource
				list := &deliveryv1alpha1.OCMDeployerList{}
				if err := r.List(ctx, list, client.MatchingFields{fieldName: resource.GetName()}); err != nil {
					return []reconcile.Request{}
				}

				// For every deployer that references the resource create a reconciliation request for that deployer
				requests := make([]reconcile.Request, 0, len(list.Items))
				for _, deployer := range list.Items {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Namespace: deployer.GetNamespace(),
							Name:      deployer.GetName(),
						},
					})
				}

				return requests
			})).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("reconciling ocmdeployer")

	ocmDeployer := &deliveryv1alpha1.OCMDeployer{}
	if err := r.Get(ctx, req.NamespacedName, ocmDeployer); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper := patch.NewSerialPatcher(ocmDeployer, r.Client)

	if ocmDeployer.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	if !ocmDeployer.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, errors.New("ocm deployer is being deleted")
	}

	octx := ocmctx.New(datacontext.MODE_EXTENDED)
	session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

	configs, err := ocm.GetEffectiveConfig(ctx, r.GetClient(), ocmDeployer)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), ocmDeployer, deliveryv1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	err = ocm.ConfigureContext(ctx, octx, r.GetClient(), configs)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), ocmDeployer, deliveryv1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	resNamespace := ocmDeployer.Spec.ResourceRef.Namespace
	if resNamespace == "" {
		// TODO: Check this out
		resNamespace = "default"
	}

	resource, err := util.GetReadyObject[deliveryv1alpha1.Resource, *deliveryv1alpha1.Resource](ctx, r.Client, client.ObjectKey{
		Namespace: resNamespace,
		Name:      ocmDeployer.Spec.ResourceRef.Name,
	})
	if err != nil {
		status.MarkNotReady(r.EventRecorder, ocmDeployer, deliveryv1alpha1.ResourceIsNotReadyReason, "Resource is not ready")

		if errors.Is(err, util.NotReadyError{}) || errors.Is(err, util.DeletionError{}) {
			logger.V(1).Info(err.Error())

			// return no requeue as we watch the object for changes anyway
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get ready resource: %w", err)
	}

	// Download the resource
	cv, err := ocm.GetComponentVersion(octx, session, resource.Status.Component.RepositorySpec.Raw, resource.Status.Component.Component, resource.Status.Component.Version)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, ocmDeployer, deliveryv1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get component version: %w", err)
	}

	resourceReference := v1.ResourceReference{
		Resource:      resource.Spec.Resource.ByReference.Resource,
		ReferencePath: resource.Spec.Resource.ByReference.ReferencePath,
	}

	a := cv.GetDescriptor()
	resourceAccess, _, err := ocm.GetResourceAccessForComponentVersion(cv, resourceReference, &ocm.Descriptors{List: []*compdesc.ComponentDescriptor{a}})
	if err != nil {
		status.MarkNotReady(r.EventRecorder, ocmDeployer, deliveryv1alpha1.GetResourceAccessFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get resource access: %w", err)
	}

	rgdManifest, digest, err := ocm.GetResource(cv, resourceAccess)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, ocmDeployer, deliveryv1alpha1.GetResourceFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get resource graph definition manifest: %w", err)
	}

	if resource.Status.Resource.Digest != digest {
		status.MarkNotReady(r.EventRecorder, ocmDeployer, deliveryv1alpha1.GetResourceFailedReason, "resource digest mismatch")

		return ctrl.Result{}, fmt.Errorf("resource digest mismatch: expected %s, got %s", resource.Status.Resource.Digest, digest)
	}

	// 2. Apply RGD resource
	var rgd krov1alpha1.ResourceGraphDefinition
	// Unmarshal the manifest into the ResourceGraphDefinition object
	if err := yaml.Unmarshal(rgdManifest, &rgd); err != nil {
		status.MarkNotReady(r.EventRecorder, ocmDeployer, deliveryv1alpha1.MarshalFailedReason, "unmarshal failed")

		return ctrl.Result{}, fmt.Errorf("failed to unmarshal manifest: %w", err)
	}

	// Create or update the object in the cluster
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &rgd, func() error {
		if err := controllerutil.SetControllerReference(ocmDeployer, &rgd, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference on resource graph definition: %w", err)
		}

		return nil
	})
	if err != nil {
		status.MarkNotReady(r.EventRecorder, ocmDeployer, deliveryv1alpha1.CreateOrUpdateFailedReason, "create or update failed")

		return ctrl.Result{}, fmt.Errorf("failed to create or update resource graph definition: %w", err)
	}

	// TODO: Drift detection required?

	status.MarkReady(r.EventRecorder, ocmDeployer, "Applied version %s", resourceAccess.Meta().GetVersion())

	if err := status.UpdateStatus(ctx, patchHelper, ocmDeployer, r.EventRecorder, ocmDeployer.GetRequeueAfter(), err); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: resource.GetRequeueAfter()}, nil
}

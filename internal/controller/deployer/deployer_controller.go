package deployer

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

// Reconciler reconciles a Deployer object.
type Reconciler struct {
	*ocm.BaseReconciler
}

var _ ocm.Reconciler = (*Reconciler)(nil)

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=deployers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=deployers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=deployers/finalizers,verbs=update
// +kubebuilder:rbac:groups=kro.run,resources=resourcegraphdefinitions,verbs=list;watch;create;update;patch

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Build index for deployers that reference a resource to get notified about resource changes.
	const fieldName = "Deployer.spec.resourceRef"
	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&deliveryv1alpha1.Deployer{},
		fieldName,
		func(obj client.Object) []string {
			deployer, ok := obj.(*deliveryv1alpha1.Deployer)
			if !ok {
				return nil
			}

			return []string{fmt.Sprintf(
				"%s/%s",
				deployer.Spec.ResourceRef.Namespace,
				deployer.Spec.ResourceRef.Name,
			)}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Deployer{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// Watch for events from OCM resources that are referenced by the deployer
		Watches(
			&deliveryv1alpha1.Resource{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				resource, ok := obj.(*deliveryv1alpha1.Resource)
				if !ok {
					return []reconcile.Request{}
				}

				// Get list of deployers that reference the resource
				list := &deliveryv1alpha1.DeployerList{}
				if err := r.List(
					ctx,
					list,
					client.MatchingFields{fieldName: client.ObjectKeyFromObject(resource).String()},
				); err != nil {
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

//nolint:funlen // we do not want to cut the function at arbitrary points
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("starting reconciliation")

	deployer := &deliveryv1alpha1.Deployer{}
	if err := r.Get(ctx, req.NamespacedName, deployer); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper := patch.NewSerialPatcher(deployer, r.Client)
	defer func(ctx context.Context) {
		err = status.UpdateStatus(ctx, patchHelper, deployer, r.EventRecorder, deployer.GetRequeueAfter(), err)
	}(ctx)

	if deployer.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	if !deployer.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, errors.New("deployer is being deleted")
	}

	octx := ocmctx.New(datacontext.MODE_EXTENDED)
	session := ocmctx.NewSession(datacontext.NewSession())
	defer func() {
		err = octx.Finalize()
	}()

	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

	configs, err := ocm.GetEffectiveConfig(ctx, r.GetClient(), deployer)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), deployer, deliveryv1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get effective config: %w", err)
	}

	err = ocm.ConfigureContext(ctx, octx, r.GetClient(), configs)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), deployer, deliveryv1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to configure context: %w", err)
	}

	resourceNamespace := deployer.Spec.ResourceRef.Namespace
	if resourceNamespace == "" {
		resourceNamespace = deployer.GetNamespace()
	}

	resource, err := util.GetReadyObject[deliveryv1alpha1.Resource, *deliveryv1alpha1.Resource](ctx, r.Client, client.ObjectKey{
		Namespace: resourceNamespace,
		Name:      deployer.Spec.ResourceRef.Name,
	})
	if err != nil {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.ResourceIsNotAvailable, err.Error())

		if errors.Is(err, util.NotReadyError{}) || errors.Is(err, util.DeletionError{}) {
			logger.Info("stop reconciling as the resource is not available", "error", err.Error())

			// return no requeue as we watch the object for changes anyway
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get ready resource: %w", err)
	}

	// Download the resource
	cv, err := ocm.GetComponentVersion(
		octx,
		session,
		resource.Status.Component.RepositorySpec.Raw,
		resource.Status.Component.Component,
		resource.Status.Component.Version,
	)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get component version: %w", err)
	}

	resourceReference := v1.ResourceReference{
		Resource:      resource.Spec.Resource.ByReference.Resource,
		ReferencePath: resource.Spec.Resource.ByReference.ReferencePath,
	}

	a := cv.GetDescriptor()
	resourceAccess, _, err := ocm.GetResourceAccessForComponentVersion(
		cv,
		resourceReference,
		&ocm.Descriptors{List: []*compdesc.ComponentDescriptor{a}},
	)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.GetOCMResourceFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get resource access: %w", err)
	}

	rgdManifest, digest, err := ocm.GetResource(cv, resourceAccess)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.GetResourceFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get resource graph definition manifest: %w", err)
	}

	if resource.Status.Resource.Digest != digest {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.GetResourceFailedReason, "resource digest mismatch")

		return ctrl.Result{}, fmt.Errorf("resource digest mismatch: expected %s, got %s", resource.Status.Resource.Digest, digest)
	}

	// Apply, Update, or Delete RGD
	var rgd krov1alpha1.ResourceGraphDefinition
	// Unmarshal the manifest into the ResourceGraphDefinition object
	if err := yaml.Unmarshal(rgdManifest, &rgd); err != nil {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.MarshalFailedReason, "unmarshal failed")

		return ctrl.Result{}, fmt.Errorf("failed to unmarshal manifest: %w", err)
	}

	// TODO: Improve deployer maturity (@frewilhelm)
	//  - https://github.com/open-component-model/ocm-k8s-toolkit/issues/194 (@frewilhelm)
	//  - https://github.com/open-component-model/ocm-k8s-toolkit/issues/195 (@frewilhelm)
	//  - https://github.com/open-component-model/ocm-k8s-toolkit/issues/196 (@frewilhelm)

	// Create or update the object in the cluster
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &rgd, func() error {
		if err := controllerutil.SetControllerReference(deployer, &rgd, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference on resource graph definition: %w", err)
		}

		return nil
	})
	if err != nil {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.CreateOrUpdateFailedReason, "create or update failed")

		return ctrl.Result{}, fmt.Errorf("failed to create or update resource graph definition: %w", err)
	}

	// TODO: Status propagation of RGD status to deployer
	//       (see https://github.com/open-component-model/ocm-k8s-toolkit/issues/192)
	status.MarkReady(r.EventRecorder, deployer, "Applied version %s", resourceAccess.Meta().GetVersion())

	return ctrl.Result{RequeueAfter: resource.GetRequeueAfter()}, nil
}

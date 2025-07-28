package deployer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/deployer/dynamic"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	"ocm.software/ocm/api/datacontext"
	"ocm.software/ocm/api/ocm/compdesc"
	"ocm.software/ocm/api/ocm/extensions/attrs/signingattr"
	"ocm.software/ocm/api/ocm/resolvers"
	"ocm.software/ocm/api/ocm/tools/signing"

	ctrl "sigs.k8s.io/controller-runtime"

	ocmctx "ocm.software/ocm/api/ocm"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/status"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/util"
)

const resourceWatchFinalizer = "watch.delivery.ocm.software"
const managedByLabel = "app.kubernetes.io/managed-by"
const versionLabel = "app.kubernetes.io/version"
const partOfLabel = "app.kubernetes.io/part-of"
const componentLabel = "app.kubernetes.io/component"
const instanceLabel = "app.kubernetes.io/instance"
const nameLabel = "app.kubernetes.io/name"
const manager = "deployer.ocm.software"

// Reconciler reconciles a Deployer object.
type Reconciler struct {
	*ocm.BaseReconciler

	resourceWatchChannel, stopResourceWatchChannel chan client.Object
	resourceWatchHasSynced                         func(obj client.Object) bool
	resourceWatchIsStopped                         func(obj client.Object) bool
}

var _ ocm.Reconciler = (*Reconciler)(nil)

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=deployers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=deployers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=deployers/finalizers,verbs=update
// +kubebuilder:rbac:groups=kro.run,resources=resourcegraphdefinitions,verbs=list;watch;create;update;patch

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	informerManager, err := r.setupDynamicResourceWatcherWithManager(mgr)
	if err != nil {
		return err
	}

	// Build index for deployers that reference a resource to get notified about resource changes.
	const fieldName = ".spec.resourceRef"
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
		WatchesRawSource(informerManager.Source()).
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

func (r *Reconciler) setupDynamicResourceWatcherWithManager(mgr ctrl.Manager) (*dynamic.InformerManager, error) {
	// only register watches for resources that are managed by the deployer controller
	sel, err := labels.Parse(fmt.Sprintf("%s=%s", managedByLabel, manager))
	if err != nil {
		return nil, fmt.Errorf("failed to parse label selector: %w", err)
	}
	// Create an event handler that will enqueue requests for deployers when a resource is updated.
	// This is used to trigger a reconciliation of the deployer when a resource is updated that is referenced by the deployer.
	eventHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
		ctrl.LoggerFrom(ctx).Info("received update from deployed resource",
			"name", object.GetName(),
			"gvk", object.GetObjectKind().GroupVersionKind().String(),
		)
		controller := metav1.GetControllerOfNoCopy(object)
		if controller == nil ||
			controller.APIVersion != deliveryv1alpha1.GroupVersion.String() ||
			controller.Kind != "Deployer" {
			return nil
		}
		return []reconcile.Request{
			{
				NamespacedName: types.NamespacedName{
					Namespace: object.GetNamespace(),
					Name:      controller.Name,
				},
			},
		}
	})

	// Here we store the dynamic resource informer manager cache.
	// This is separate from the controller manager cache, so we can use it to register and unregister watches for resources
	// that are referenced by the deployer dynamically.
	resourceManagerCache, err := cache.New(mgr.GetConfig(), cache.Options{
		HTTPClient:                   mgr.GetHTTPClient(),
		Scheme:                       mgr.GetScheme(),
		Mapper:                       mgr.GetRESTMapper(),
		ReaderFailOnMissingInformer:  true,
		DefaultLabelSelector:         sel,
		DefaultTransform:             dynamic.TransformPartialObjectMetadata,
		DefaultUnsafeDisableDeepCopy: ptr.To(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create cache for dynamic resource informer manager: %w", err)
	}

	if err := mgr.Add(resourceManagerCache); err != nil {
		return nil, fmt.Errorf("failed to add dynamic informer manager cache to controller manager: %w", err)
	}

	// For Registering and Unregistering watches, we use a dynamic informer manager.
	// To buffer pending registrations and unregistrations, we use channels.
	informerManager := dynamic.NewInformerManager(resourceManagerCache, eventHandler)
	// this channel is used to register watches for resources that are referenced by the deployer.
	r.resourceWatchChannel = informerManager.RegisterChannel()
	// this channel is used to unregister watches for resources that are referenced by the deployer.
	r.stopResourceWatchChannel = informerManager.UnregisterChannel()
	// The resourceWatchHasSynced function is used to check if a resource is already registered and synced once requested.
	r.resourceWatchHasSynced = informerManager.HasSynced
	// The resourceWatchIsStopped function is used to check if a resource watch is stopped. useful for cleanup purposes.
	r.resourceWatchIsStopped = informerManager.IsStopped
	// Add the dynamic informer manager to the controller manager. This will make the dynamic informer manager start
	// its registration and unregistration workers once the controller manager is started.
	if err := mgr.Add(informerManager); err != nil {
		return nil, fmt.Errorf("failed to add dynamic informer manager to controller manager: %w", err)
	}
	return informerManager, nil
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
		err = errors.Join(err, status.UpdateStatus(ctx, patchHelper, deployer, r.EventRecorder, time.Second, err))
	}(ctx)

	if deployer.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	if !deployer.GetDeletionTimestamp().IsZero() {
		var atLeastOneFinalizerRemoved bool
		for _, deployed := range deployer.Status.Deployed {
			obj := deployedObjectReferenceToObject(deployed)
			if !r.resourceWatchIsStopped(obj) {
				logger.Info("unregistering resource watch for deployer", "name", deployer.GetName())
				r.stopResourceWatchChannel <- obj
				return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
			}
			removed := controllerutil.RemoveFinalizer(deployer, resourceWatchFinalizer+"/"+string(obj.GetUID()))
			if !atLeastOneFinalizerRemoved && removed {
				atLeastOneFinalizerRemoved = true
			}
		}

		if atLeastOneFinalizerRemoved {
			if err := r.Update(ctx, deployer, &client.UpdateOptions{}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from deployer: %w", err)
			} else {
				logger.Info("removed finalizer from deployer", "name", deployer.GetName())
			}
		}

		return ctrl.Result{}, nil
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
	spec, err := octx.RepositorySpecForConfig(resource.Status.Component.RepositorySpec.Raw, nil)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get repository spec: %w", err)
	}

	repo, err := session.LookupRepository(octx, spec)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("invalid repository spec: %w", err)
	}

	cv, err := session.LookupComponentVersion(repo, resource.Status.Component.Component, resource.Status.Component.Version)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get component version: %w", err)
	}

	resourceReference := v1.ResourceReference{
		Resource:      resource.Spec.Resource.ByReference.Resource,
		ReferencePath: resource.Spec.Resource.ByReference.ReferencePath,
	}

	resourceAccess, _, err := ocm.GetResourceAccessForComponentVersion(
		ctx,
		session,
		cv,
		resourceReference,
		&ocm.Descriptors{List: []*compdesc.ComponentDescriptor{cv.GetDescriptor()}},
		resolvers.NewCompoundResolver(repo, octx.GetResolver()),
		resource.Spec.SkipVerify,
	)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.GetOCMResourceFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get resource access: %w", err)
	}

	// Get the resource graph definition manifest and its digest. Compare the digest to the one in the resource to make
	// sure the resource is up to date.
	manifest, digest, err := getResource(cv, resourceAccess)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.GetOCMResourceFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get resource graph definition manifest: %w", err)
	}

	if resource.Status.Resource.Digest != digest {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.GetOCMResourceFailedReason, "resource digest mismatch")

		return ctrl.Result{}, fmt.Errorf("resource digest mismatch: expected %s, got %s", resource.Status.Resource.Digest, digest)
	}

	// Apply, Update, or Delete Object
	var obj *unstructured.Unstructured
	// Unmarshal the manifest into the ResourceGraphDefinition object
	if err := yaml.Unmarshal(manifest, &obj); err != nil {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.MarshalFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to unmarshal manifest: %w", err)
	}
	if r.resourceWatchHasSynced(obj) {
		logger.Info("resource graph definition is already registered and synced, skipping registration")
	} else {
		logger.Info("registering watch from deployer", "obj", obj.GetName())
		r.resourceWatchChannel <- obj
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.ResourceNotSynced, "resource is not registered and synced")

		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// TODO: Improve deployer maturity (@frewilhelm)
	//  - https://github.com/open-component-model/ocm-k8s-toolkit/issues/195 (@frewilhelm)

	if err := controllerutil.SetControllerReference(deployer, obj, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set controller reference on resource graph definition: %w", err)
	}

	var lbls map[string]string
	if obj.GetLabels() != nil {
		lbls = obj.GetLabels()
	} else {
		lbls = make(map[string]string)
	}

	lbls[nameLabel] = resource.Status.Resource.Name
	lbls[versionLabel] = resource.Status.Resource.Version
	lbls[instanceLabel] = string(deployer.GetUID())
	lbls[partOfLabel] = deployer.GetName()
	lbls[managedByLabel] = manager
	obj.SetLabels(lbls)

	// TODO: We may want to compute a diff here based on the cache content. This would allow us to only apply the changes
	//       instead of applying the whole object every time, as this can be expensive for large objects on the API server.
	if err := r.GetClient().Patch(ctx, obj, client.Apply, &client.PatchOptions{
		Force:           ptr.To(true),
		FieldManager:    fmt.Sprintf("%s/%s", manager, deployer.UID),
		FieldValidation: metav1.FieldValidationWarn,
	}); err != nil {
		status.MarkNotReady(r.EventRecorder, deployer, deliveryv1alpha1.CreateOrUpdateFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to set controller reference on resource graph definition: %w", err)
	}

	updateDeployedObjectReferences(obj, deployer)

	logger.Info("applied object", "name", obj.GetName())

	// TODO: Status propagation of RGD status to deployer
	//       (see https://github.com/open-component-model/ocm-k8s-toolkit/issues/192)
	status.MarkReady(r.EventRecorder, deployer, "Applied version %s", resourceAccess.Meta().GetVersion())

	return ctrl.Result{}, nil
}

func deployedObjectReferenceToObject(deployed deliveryv1alpha1.DeployedObjectReference) client.Object {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(deployed.APIVersion)
	obj.SetKind(deployed.Kind)
	obj.SetName(deployed.Name)
	obj.SetNamespace(deployed.Namespace)
	obj.SetUID(deployed.UID)
	return obj
}

func updateDeployedObjectReferences(obj *unstructured.Unstructured, deployer *deliveryv1alpha1.Deployer) {
	ref := deliveryv1alpha1.DeployedObjectReference{
		Name:       obj.GetName(),
		Namespace:  obj.GetNamespace(),
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		UID:        obj.GetUID(),
	}
	if idx := slices.IndexFunc(deployer.Status.Deployed, func(reference deliveryv1alpha1.DeployedObjectReference) bool {
		if reference.UID == obj.GetUID() {
			return true
		}
		return false
	}); idx < 0 {
		deployer.Status.Deployed = append(deployer.Status.Deployed, ref)
	} else {
		deployer.Status.Deployed[idx] = ref
	}
	controllerutil.AddFinalizer(deployer, resourceWatchFinalizer+"/"+string(obj.GetUID()))
}

// getResource returns the resource data as byte-slice and its digest.
func getResource(cv ocmctx.ComponentVersionAccess, resourceAccess ocmctx.ResourceAccess) ([]byte, string, error) {
	octx := cv.GetContext()
	cd := cv.GetDescriptor()
	raw := &cd.Resources[cd.GetResourceIndex(resourceAccess.Meta())]

	if raw.Digest == nil {
		return nil, "", errors.New("digest not found in resource access")
	}

	// Check if the resource is signature relevant and calculate digest of resource
	acc, err := octx.AccessSpecForSpec(raw.Access)
	if err != nil {
		return nil, "", fmt.Errorf("failed getting access for resource: %w", err)
	}

	meth, err := acc.AccessMethod(cv)
	if err != nil {
		return nil, "", fmt.Errorf("failed getting access method: %w", err)
	}

	accessMethod, err := resourceAccess.AccessMethod()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create access method: %w", err)
	}

	bAcc := accessMethod.AsBlobAccess()

	meth = signing.NewRedirectedAccessMethod(meth, bAcc)
	resAccDigest := raw.Digest
	resAccDigestType := signing.DigesterType(resAccDigest)
	req := []ocmctx.DigesterType{resAccDigestType}

	registry := signingattr.Get(octx).HandlerRegistry()
	hasher := registry.GetHasher(resAccDigestType.HashAlgorithm)
	digest, err := octx.BlobDigesters().DetermineDigests(raw.Type, hasher, registry, meth, req...)
	if err != nil {
		return nil, "", fmt.Errorf("failed determining digest for resource: %w", err)
	}

	// Get actual resource data
	data, err := bAcc.Get()
	if err != nil {
		return nil, "", fmt.Errorf("failed getting resource data: %w", err)
	}

	return data, digest[0].String(), nil
}

package localization

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"ocm.software/ocm/api/ocm/compdesc"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/localblob"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociblob"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ocmctx "ocm.software/ocm/api/ocm"
	ocmmetav1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	localizationclient "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/client"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/types"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/index"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	snapshotRegistry "github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/util"
)

// Reconciler reconciles a LocalizationRules object.
type Reconciler struct {
	*ocm.BaseReconciler
	LocalizationClient localizationclient.Client
	Registry           snapshotRegistry.RegistryType
}

var _ ocm.Reconciler = (*Reconciler)(nil)

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	onTargetChange, onConfigChange, err := index.TargetAndConfig[v1alpha1.LocalizedResource](mgr)
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.LocalizedResource{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// Update when the owned ConfiguredResource changes
		Owns(&v1alpha1.ConfiguredResource{}).
		// Update when a resource specified as target changes
		Watches(&v1alpha1.Resource{}, onTargetChange).
		Watches(&v1alpha1.LocalizedResource{}, onTargetChange).
		Watches(&v1alpha1.ConfiguredResource{}, onTargetChange).
		// Update when a localization config coming from a resource changes
		Watches(&v1alpha1.Resource{}, onConfigChange).
		Watches(&v1alpha1.LocalizedResource{}, onConfigChange).
		Watches(&v1alpha1.ConfiguredResource{}, onConfigChange).
		// Update when a localization config coming from the cluster changes
		Watches(&v1alpha1.LocalizationConfig{}, onConfigChange).
		Named("localizedresource").
		Complete(r)
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizedresources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizedresources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizedresources/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizationconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=resourceconfigs,verbs=get;list;watch;create;update;patch

// +kubebuilder:rbac:groups="",resources=secrets;configmaps;serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)

	localization := &v1alpha1.LocalizedResource{}
	if err := r.Get(ctx, req.NamespacedName, localization); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if localization.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	if localization.GetDeletionTimestamp() != nil {
		logger.Info("localization is being deleted and cannot be used", "name", localization.Name)

		return ctrl.Result{}, nil
	}

	return r.reconcileWithStatusUpdate(ctx, localization)
}

func (r *Reconciler) reconcileWithStatusUpdate(ctx context.Context, localization *v1alpha1.LocalizedResource) (ctrl.Result, error) {
	patchHelper := patch.NewSerialPatcher(localization, r.Client)

	result, err := r.reconcileExists(ctx, localization)

	if err = errors.Join(
		err,
		status.UpdateStatus(ctx, patchHelper, localization, r.EventRecorder, localization.Spec.Interval.Duration, err),
	); err != nil {
		return ctrl.Result{}, err
	}

	return result, nil
}

func (r *Reconciler) reconcileExists(ctx context.Context, localization *v1alpha1.LocalizedResource) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if localization.Spec.Target.Namespace == "" {
		localization.Spec.Target.Namespace = localization.Namespace
	}

	target, err := r.LocalizationClient.GetLocalizationTarget(ctx, localization.Spec.Target)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, localization, v1alpha1.TargetFetchFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to fetch target: %w", err)
	}

	targetBackedByComponent, ok := target.(LocalizableSnapshotContent)
	if !ok {
		err = fmt.Errorf("target is not backed by a component and cannot be localized")
		status.MarkNotReady(r.EventRecorder, localization, v1alpha1.TargetFetchFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	if localization.Spec.Config.Namespace == "" {
		localization.Spec.Config.Namespace = localization.Namespace
	}

	cfg, err := r.LocalizationClient.GetLocalizationConfig(ctx, localization.Spec.Config)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, localization, v1alpha1.ConfigFetchFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to fetch config: %w", err)
	}

	rules, err := localizeRules(ctx, r.Client, r.Registry, targetBackedByComponent, cfg)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, localization, v1alpha1.LocalizationRuleGenerationFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to localize rules: %w", err)
	}

	resourceConfig := &v1alpha1.ResourceConfig{
		ObjectMeta: v1.ObjectMeta{
			Name:      localization.GetName(),
			Namespace: localization.GetNamespace(),
		},
	}

	resCfgOp, err := controllerutil.CreateOrUpdate(ctx, r.Client, resourceConfig, func() error {
		if err := controllerutil.SetControllerReference(localization, resourceConfig, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference on resource config: %w", err)
		}
		resourceConfig.Spec = v1alpha1.ResourceConfigSpec{
			Rules: rules,
		}

		return nil
	})
	if err != nil {
		status.MarkNotReady(r.EventRecorder, localization, v1alpha1.ConfigGenerationFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to create or update resource config: %w", err)
	}
	logger.V(1).Info(fmt.Sprintf("resource config %s", resCfgOp))

	configuredResource := &v1alpha1.ConfiguredResource{
		ObjectMeta: v1.ObjectMeta{
			Name:      localization.GetName(),
			Namespace: localization.GetNamespace(),
		},
	}

	confResOp, err := controllerutil.CreateOrUpdate(ctx, r.Client, configuredResource, func() error {
		if err := controllerutil.SetControllerReference(localization, configuredResource, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference on configured resource: %w", err)
		}
		gvk, err := apiutil.GVKForObject(resourceConfig, r.Scheme)
		if err != nil {
			return fmt.Errorf("failed to get GVK for resource config: %w", err)
		}
		apiVersion, kind := gvk.ToAPIVersionAndKind()
		configuredResource.Spec = v1alpha1.ConfiguredResourceSpec{
			Target: *localization.Spec.Target.DeepCopy(),
			Config: v1alpha1.ConfigurationReference{
				NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
					APIVersion: apiVersion,
					Kind:       kind,
					Name:       resourceConfig.GetName(),
					Namespace:  resourceConfig.GetNamespace(),
				},
			},
			Interval: localization.Spec.Interval,
		}

		return nil
	})
	if err != nil {
		status.MarkNotReady(r.EventRecorder, localization, v1alpha1.ResourceGenerationFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to create or update configured resource: %w", err)
	}
	logger.V(1).Info(fmt.Sprintf("configured resource %s", confResOp))

	if !conditions.IsReady(configuredResource) {
		status.MarkNotReady(
			r.EventRecorder,
			localization,
			v1alpha1.LocalizationIsNotReadyReason,
			"configured resource containing localized content is not yet ready",
		)

		return ctrl.Result{}, fmt.Errorf("configured resource containing localization is not yet ready")
	}

	snapshotCR, err := snapshotRegistry.GetSnapshotForOwner(ctx, r.Client, configuredResource)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, localization, v1alpha1.GetSnapshotFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	snapshotOp, err := controllerutil.CreateOrUpdate(ctx, r.Client, snapshotCR, func() error {
		if err := controllerutil.SetOwnerReference(localization, snapshotCR, r.Scheme); err != nil {
			return fmt.Errorf("failed to set indirect owner reference on snapshot: %w", err)
		}

		if snapshotCR.GetAnnotations() == nil {
			snapshotCR.SetAnnotations(map[string]string{})
		}
		a := snapshotCR.GetAnnotations()
		a["ocm.software/snapshot-purpose"] = "localization"
		a["ocm.software/localization"] = fmt.Sprintf("%s/%s", localization.GetNamespace(), localization.GetName())
		snapshotCR.SetAnnotations(a)

		return nil
	})
	if err != nil {
		status.MarkNotReady(r.EventRecorder, localization, v1alpha1.ReconcileArtifactFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to create or update artifact: %w", err)
	}
	logger.V(1).Info(fmt.Sprintf("snapshot %s", snapshotOp))

	localization.Status.SnapshotRef = configuredResource.Status.SnapshotRef
	localization.Status.Digest = configuredResource.Status.Digest
	localization.Status.ConfiguredResourceRef = &v1alpha1.ObjectKey{
		Name:      configuredResource.GetName(),
		Namespace: configuredResource.GetNamespace(),
	}

	status.MarkReady(r.EventRecorder, localization, "localized successfully")

	return ctrl.Result{RequeueAfter: localization.Spec.Interval.Duration}, nil
}

func localizeRules(
	ctx context.Context,
	c client.Client,
	r snapshotRegistry.RegistryType,
	content LocalizableSnapshotContent,
	cfg types.LocalizationConfig,
) (
	[]v1alpha1.ConfigurationRule,
	error,
) {
	original, err := ParseConfig(cfg, c.Scheme())
	if err != nil {
		return nil, fmt.Errorf("failed to parse localization config: %w", err)
	}

	componentSet, componentDescriptor, err := ComponentDescriptorAndSetFromResource(ctx, c, r, content.GetComponent())
	if err != nil {
		return nil, fmt.Errorf("failed to get content descriptor and set: %w", err)
	}

	rules := original.Spec.Rules

	localizedRules := make([]v1alpha1.ConfigurationRule, len(rules))
	for i, rule := range rules {
		// TODO Decide what the hell to do with GoTemplates
		if rule.GoTemplate != nil {
			localizedRules[i] = v1alpha1.ConfigurationRule{
				GoTemplate: (*v1alpha1.ConfigurationRuleGoTemplate)(rule.GoTemplate),
			}

			continue
		}

		ref := ocmmetav1.ResourceReference{
			Resource:      rule.YAMLSubstitution.Source.Resource,
			ReferencePath: rule.YAMLSubstitution.Source.ReferencePath,
		}
		resource, _, err := compdesc.ResolveResourceReference(componentDescriptor, ref, componentSet)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve resource reference in component descriptor of rule at index %v: %w", i, err)
		}

		var value string

		if value, err = resolve(resource); err != nil {
			return nil, fmt.Errorf("failed to resolve value from resource reference of rule at index %v: %w", i, err)
		}
		if value, err = transform(value, rule.YAMLSubstitution.Transformation); err != nil {
			return nil, fmt.Errorf("failed to apply transformation to resolved value of rule at index %v: %w", i, err)
		}

		localizedRules[i] = v1alpha1.ConfigurationRule{
			YAMLSubstitution: &v1alpha1.ConfigurationRuleYAMLSubstitution{
				Target: v1alpha1.ConfigurationRuleYAMLSubsitutionTarget{File: rule.YAMLSubstitution.Target.File},
				Source: v1alpha1.ConfigurationRuleYAMLSubsitutionSource{Value: value},
			},
		}
	}

	return localizedRules, nil
}

// LocalizableSnapshotContent is an artifact content that is backed by a component and resource, allowing it
// to be localized (by resolving relative references from the resource & component into absolute values).
type LocalizableSnapshotContent interface {
	artifact.Content
	GetComponent() *v1alpha1.Component
	GetResource() *v1alpha1.Resource
}

func ComponentDescriptorAndSetFromResource(
	ctx context.Context,
	clnt client.Client,
	r snapshotRegistry.RegistryType,
	baseComponent *v1alpha1.Component,
) (compdesc.ComponentVersionResolver, *compdesc.ComponentDescriptor, error) {
	snapshotResource, err := snapshotRegistry.GetSnapshotForOwner(ctx, clnt, baseComponent)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get snapshot: %w", err)
	}

	repository, err := r.NewRepository(ctx, snapshotResource.Spec.Repository)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create repository: %w", err)
	}

	componentSet, err := ocm.GetComponentSetForSnapshot(ctx, repository, snapshotResource)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get component version set: %w", err)
	}

	componentDescriptor, err := componentSet.LookupComponentVersion(baseComponent.Spec.Component, baseComponent.Status.Component.Version)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to lookup component version: %w", err)
	}

	return componentSet, componentDescriptor, nil
}

func ParseConfig(source types.LocalizationConfig, scheme *runtime.Scheme) (config *v1alpha1.LocalizationConfig, err error) {
	cfgReader, err := source.Open()
	defer func() {
		err = errors.Join(err, cfgReader.Close())
	}()

	return util.Parse[v1alpha1.LocalizationConfig](cfgReader, serializer.NewCodecFactory(scheme).UniversalDeserializer())
}

// resolve returns an access value for the resource.
// This is what actually resolves the name of the util to the actual image reference.
func resolve(
	resource *compdesc.Resource,
) (_ string, retErr error) {
	accessSpecification := resource.GetAccess()

	var (
		ref    string
		refErr error
	)

	specInCtx, err := ocmctx.DefaultContext().AccessSpecForSpec(accessSpecification)
	if err != nil {
		return "", fmt.Errorf("failed to resolve access spec: %w", err)
	}

	// TODO this seems hacky but I copy & pasted, we need to find a better way
	for ref == "" && refErr == nil {
		switch x := specInCtx.(type) {
		case *ociartifact.AccessSpec:
			ref = x.ImageReference
		case *ociblob.AccessSpec:
			ref = fmt.Sprintf("%s@%s", x.Reference, x.Digest)
		case *localblob.AccessSpec:
			if x.GlobalAccess == nil {
				refErr = errors.New("cannot determine image digest")
			} else {
				// TODO maybe this needs whole OCM Context resolution?
				// I dont think we need a localized resolution here but Im not sure
				specInCtx = x.GlobalAccess.GlobalAccessSpec(ocmctx.DefaultContext())
			}
		default:
			refErr = errors.New("cannot determine access spec type")
		}
	}
	if refErr != nil {
		return "", fmt.Errorf("failed to parse access reference: %w", refErr)
	}

	return ref, nil
}

func transform(value string, transformation v1alpha1.Transformation) (transformed string, err error) {
	parsed, err := name.ParseReference(value)
	if err != nil {
		return "", fmt.Errorf("failed to parse access reference: %w", err)
	}
	switch transformation.Type {
	case v1alpha1.TransformationTypeRegistry:
		transformed = parsed.Context().Registry.RegistryStr()
	case v1alpha1.TransformationTypeRepository:
		transformed = parsed.Context().RepositoryStr()
	case v1alpha1.TransformationTypeTag:
		transformed = parsed.Identifier()
	case v1alpha1.TransformationTypeImageNoTag:
		transformed = parsed.Context().Name()
	case v1alpha1.TransformationTypeImage:
		// By default treat the reference as a full image reference
		fallthrough
	default:
		transformed = parsed.Name()
	}

	return
}

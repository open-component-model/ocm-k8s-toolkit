package localization

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/fluxcd/pkg/runtime/patch"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	localizationclient "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/client"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/strategy/mapped"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

const (
	ReasonTargetFetchFailed        = "TargetFetchFailed"
	ReasonSourceFetchFailed        = "SourceFetchFailed"
	ReasonLocalizationFailed       = "LocalizationFailed"
	ReasonUniqueIDGenerationFailed = "UniqueIDGenerationFailed"
)

// Reconciler reconciles a LocalizationRules object.
type Reconciler struct {
	*ocm.BaseReconciler
	*storage.Storage
}

var _ ocm.Reconciler = (*Reconciler)(nil)

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.LocalizedResource{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

func (r *Reconciler) GetStorage() *storage.Storage {
	return r.Storage
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizedresources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizedresources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizedresources/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizationconfigs,verbs=get;list;watch

// +kubebuilder:rbac:groups="",resources=secrets;configmaps;serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	localization := &v1alpha1.LocalizedResource{}
	if err := r.Get(ctx, req.NamespacedName, localization); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if localization.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	if !localization.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, r.reconcileDeletion(ctx, localization)
	}

	if added := controllerutil.AddFinalizer(localization, v1alpha1.ArtifactFinalizer); added {
		return ctrl.Result{Requeue: true}, r.Update(ctx, localization)
	}

	patchHelper := patch.NewSerialPatcher(localization, r.Client)

	// Always attempt to patch the object and status after each reconciliation.
	defer func() {
		if statusErr := status.UpdateStatus(ctx, patchHelper, localization, r.EventRecorder, localization.Spec.Interval.Duration, err); statusErr != nil {
			err = errors.Join(err, statusErr)
		}
	}()

	return r.reconcileExists(ctx, localization)
}

func (r *Reconciler) reconcileDeletion(ctx context.Context, localization *v1alpha1.LocalizedResource) error {
	artifact, err := ocm.GetAndVerifyArtifactForCollectable(ctx, r, r.Storage, localization)
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get artifact: %w", err)
	}
	if artifact == nil {
		log.FromContext(ctx).Info("artifact belonging to localization not found, skipping deletion")

		return nil
	}
	if err := r.Storage.Remove(artifact); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove artifact: %w", err)
		}
	}
	if removed := controllerutil.RemoveFinalizer(localization, v1alpha1.ArtifactFinalizer); removed {
		if err := r.Update(ctx, localization); err != nil {
			return fmt.Errorf("failed to remove finalizer: %w", err)
		}
	}

	return nil
}

func (r *Reconciler) reconcileExists(ctx context.Context, localization *v1alpha1.LocalizedResource) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if err := r.Storage.ReconcileStorage(ctx, localization); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile storage: %w", err)
	}

	loc := localizationclient.NewClientWithLocalStorage(r.Client, r.Storage, r.Scheme)

	if localization.Spec.Target.Namespace == "" {
		localization.Spec.Target.Namespace = localization.Namespace
	}

	target, err := loc.GetLocalizationTarget(ctx, localization.Spec.Target)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, localization, ReasonTargetFetchFailed, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to fetch target: %w", err)
	}

	targetBackedByResource, ok := target.(ArtifactContentBackedByResource)
	if !ok {
		err = fmt.Errorf("target is not backed by a resource and cannot be localized")
		status.MarkNotReady(r.EventRecorder, localization, ReasonTargetFetchFailed, err.Error())

		return ctrl.Result{}, err
	}

	if localization.Spec.Config.Namespace == "" {
		localization.Spec.Config.Namespace = localization.Namespace
	}

	cfg, err := loc.GetLocalizationConfig(ctx, localization.Spec.Config)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, localization, ReasonSourceFetchFailed, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to fetch source: %w", err)
	}

	digest, revision, file, err := artifact.UniqueIDsForArtifactContentCombination(cfg, target)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, localization, ReasonUniqueIDGenerationFailed, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to map digest from config to target: %w", err)
	}

	hasValidArtifact, err := ocm.CollectableHasValidArtifactBasedOnFileNameDigest(
		ctx,
		r.Client,
		r.Storage,
		localization,
		digest,
	)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check if artifact is valid: %w", err)
	}

	var localized string
	if !hasValidArtifact {
		localize := func(basePath string) (string, error) {
			return mapped.Localize(ctx, loc, r.Storage, cfg, targetBackedByResource, basePath)
		}
		if localized, err = runInTempDir(localize); err != nil {
			status.MarkNotReady(r.EventRecorder, localization, ReasonLocalizationFailed, err.Error())
			logger.Error(err, "failed to localize", "interval", localization.Spec.Interval.Duration)

			return ctrl.Result{}, err
		}
	}

	localization.Status.LocalizationDigest = digest

	if err := r.Storage.ReconcileArtifact(
		ctx,
		localization,
		revision,
		localized,
		file,
		func(artifact *artifactv1.Artifact, dir string) error {
			if !hasValidArtifact {
				// Archive directory to storage
				if err := r.Storage.Archive(artifact, dir, nil); err != nil {
					return fmt.Errorf("unable to archive artifact to storage: %w", err)
				}
			}

			localization.Status.ArtifactRef = &v1alpha1.ObjectKey{
				Name:      artifact.Name,
				Namespace: artifact.Namespace,
			}

			return os.RemoveAll(dir)
		},
	); err != nil {
		status.MarkNotReady(r.EventRecorder, localization, v1alpha1.ReconcileArtifactFailedReason, err.Error())
	}

	logger.Info("localization successful", "artifact", localization.Status.ArtifactRef)
	status.MarkReady(r.EventRecorder, localization, "localized successfully")

	return ctrl.Result{RequeueAfter: localization.Spec.Interval.Duration}, nil
}

func runInTempDir(withinPath func(basePath string) (string, error)) (_ string, err error) {
	basePath, err := os.MkdirTemp("", "localization-")
	if err != nil {
		return "", fmt.Errorf("tmp dir error: %w", err)
	}
	defer func() {
		err = errors.Join(err, os.RemoveAll(basePath))
	}()
	return withinPath(basePath)
}

// ArtifactContentBackedByResource is an artifact content that is backed by a resource.
// TODO This is currently only necessary because we introspect the relationship of the artifact content to the resource.
// We do this to determine the context of the localization so that we can draw the correct component descriptor
// from the underlying component version.
// If we remove this introspection, we can remove this interface but need to find another way to get the introspection
// working.
type ArtifactContentBackedByResource interface {
	artifact.Content
	GetResource() *v1alpha1.Resource
}

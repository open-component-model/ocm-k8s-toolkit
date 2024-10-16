package localization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/opencontainers/go-digest"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	localizationclient "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/client"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/strategy/mapped"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/types"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

const (
	ReasonTargetFetchFailed  = "TargetFetchFailed"
	ReasonSourceFetchFailed  = "SourceFetchFailed"
	ReasonLocalizationFailed = "LocalizationFailed"
)

var (
	ErrUnsupportedLocalizationStrategy = errors.New("unsupported localization strategy")
	ErrOnlyOneLocalizationStrategy     = errors.New("only one localization strategy is supported")
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

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizations,verbs=get;list;watch;create;update;patch_test;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizations/status,verbs=get;update;patch_test
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizations/finalizers,verbs=update

// +kubebuilder:rbac:groups="",resources=secrets;configmaps;serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch_test

// Reconcile the component object.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	localization := &v1alpha1.LocalizedResource{}
	if err := r.Get(ctx, req.NamespacedName, localization); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if localization.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	// no need for a dedicated finalizer since gc should also be able to take care of it
	// TODO check if that actually happens or if we in fact do need a finalizer
	if !localization.GetDeletionTimestamp().IsZero() {
		artifact, err := ocm.GetAndVerifyArtifactForCollectable(ctx, r, r.Storage, localization)
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get artifact: %w", err)
		}
		if artifact == nil {
			log.FromContext(ctx).Info("artifact belonging to localization not found, skipping deletion")

			return ctrl.Result{}, nil
		}
		if err := r.Storage.Remove(*artifact); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove artifact: %w", err)
		}
	}

	patchHelper := patch.NewSerialPatcher(localization, r.Client)

	// Always attempt to patch_test the object and status after each reconciliation.
	defer func() {
		if statusErr := status.UpdateStatus(ctx, patchHelper, localization, r.EventRecorder, localization.Spec.Interval.Duration, err); statusErr != nil {
			err = errors.Join(err, statusErr)
		}
	}()

	return r.reconcileExists(ctx, localization)
}

func (r *Reconciler) reconcileExists(ctx context.Context, localization *v1alpha1.LocalizedResource) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if err := r.Storage.ReconcileStorage(ctx, localization); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile storage: %w", err)
	}

	loc := localizationclient.NewClientWithLocalStorage(r.Client, r.Storage)

	if localization.Spec.Target.Namespace == "" {
		localization.Spec.Target.Namespace = localization.Namespace
	}

	target, err := loc.GetLocalizationTarget(ctx, localization.Spec.Target)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, localization, ReasonTargetFetchFailed, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to fetch target: %w", err)
	}

	if localization.Spec.Source.Namespace == "" {
		localization.Spec.Source.Namespace = localization.Namespace
	}

	source, err := loc.GetLocalizationSource(ctx, localization.Spec.Source)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, localization, ReasonSourceFetchFailed, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to fetch source: %w", err)
	}

	revisionAndDigest := NewMappedRevisionAndDigest(source, target)

	hasValidArtifact, err := hasValidArtifact(ctx, r.Client, r.Storage, localization, revisionAndDigest)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check if artifact is valid: %w", err)
	}

	var localized string
	if !hasValidArtifact {
		if localized, err = r.localize(ctx, source, target); err != nil {
			status.MarkNotReady(r.EventRecorder, localization, ReasonLocalizationFailed, err.Error())
			logger.Error(err, "failed to localize, retrying later", "interval", localization.Spec.Interval.Duration)

			return ctrl.Result{RequeueAfter: localization.Spec.Interval.Duration}, nil
		}
	}

	localization.Status.LocalizationDigest = revisionAndDigest.Digest()

	if err := r.Storage.ReconcileArtifact(
		ctx,
		localization,
		revisionAndDigest.Revision(),
		localized,
		revisionAndDigest.ToArchiveFileName(),
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

type RenderTarget string

func (r *Reconciler) localize(ctx context.Context,
	src types.LocalizationSourceWithStrategy,
	trgt types.LocalizationTarget,
) (string, error) {
	strategy := src.GetStrategy()
	instructions := 0
	var localize func(ctx context.Context, src types.LocalizationSourceWithStrategy, trgt types.LocalizationTarget) (string, error)

	if strategy.Mapped != nil {
		instructions++
		localize = func(ctx context.Context, src types.LocalizationSourceWithStrategy, trgt types.LocalizationTarget) (string, error) {
			return mapped.Localize(ctx, localizationclient.NewClientWithLocalStorage(r.Client, r.Storage), r.Storage, src, trgt)
		}
	}

	if instructions == 0 {
		return "", fmt.Errorf("%w: %v", ErrUnsupportedLocalizationStrategy, strategy)
	} else if instructions > 1 {
		return "", fmt.Errorf("%w: %v", ErrOnlyOneLocalizationStrategy, strategy)
	}

	return localize(ctx, src, trgt)
}

func NewMappedRevisionAndDigest(source types.LocalizationSourceWithStrategy, target types.LocalizationTarget) MappedRevisionAndDigest {
	return MappedRevisionAndDigest{
		SourceRevision:          source.GetRevision(),
		StrategyCustomizationID: digest.FromString(encodeJSONToString(source.GetStrategy())).String(),
		SourceDigest:            source.GetDigest(),
		TargetRevision:          target.GetRevision(),
		TargetDigest:            target.GetDigest(),
	}
}

type MappedRevisionAndDigest struct {
	SourceRevision          string `json:"source"`
	TargetRevision          string `json:"target"`
	StrategyCustomizationID string `json:"strategy"`
	SourceDigest            string `json:"-"`
	TargetDigest            string `json:"-"`
}

func (r MappedRevisionAndDigest) String() string {
	return encodeJSONToString(r)
}

func encodeJSONToString[T any](toEncode T) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	if err := encoder.Encode(toEncode); err != nil {
		return "invalid mapped revision: " + err.Error()
	}

	return buf.String()
}

func (r MappedRevisionAndDigest) Digest() string {
	return digest.FromString(r.SourceDigest + r.TargetDigest + r.StrategyCustomizationID).String()
}

func (r MappedRevisionAndDigest) Revision() string {
	return fmt.Sprintf("%s localized with %s (strategy customization id %s)", r.SourceRevision, r.TargetRevision, r.StrategyCustomizationID)
}

func (r MappedRevisionAndDigest) ToArchiveFileName() string {
	return strings.ReplaceAll(r.Digest(), ":", "_") + ".tar.gz"
}

func hasValidArtifact(ctx context.Context, reader client.Reader, strg *storage.Storage, localization *v1alpha1.LocalizedResource, revisionAndDigest MappedRevisionAndDigest) (bool, error) {
	artifact, err := ocm.GetAndVerifyArtifactForCollectable(ctx, reader, strg, localization)
	if client.IgnoreNotFound(err) != nil {
		return false, fmt.Errorf("failed to get artifact: %w", err)
	}
	if artifact == nil {
		return false, nil
	}

	existingFile := filepath.Base(strg.LocalPath(*artifact))

	return existingFile != revisionAndDigest.Digest(), nil
}

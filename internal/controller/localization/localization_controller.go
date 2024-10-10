package localization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/strategy/kustomize_patch"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/types"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

const (
	ReasonTargetFetchFailed  = "TargetFetchFailed"
	ReasonSourceFetchFailed  = "SourceFetchFailed"
	ReasonLocalizationFailed = "LocalizationFailed"
)

var ErrUnsupportedLocalizationStrategy = errors.New("unsupported localization strategy")

// Reconciler reconciles a Localization object.
type Reconciler struct {
	*ocm.BaseReconciler
	*storage.Storage
}

var _ ocm.Reconciler = (*Reconciler)(nil)

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Localization{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
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
	localization := &v1alpha1.Localization{}
	if err := r.Get(ctx, req.NamespacedName, localization); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if localization.Spec.Suspend {
		return ctrl.Result{}, nil
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

func (r *Reconciler) reconcileExists(ctx context.Context, localization *v1alpha1.Localization) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	loc := NewClientWithLocalStorage(r.Client, r.Storage)

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

	localized, err := r.localize(ctx, source, target)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, localization, ReasonLocalizationFailed, err.Error())
		logger.Error(err, "failed to localize, retrying later", "interval", localization.Spec.Interval.Duration)
		return ctrl.Result{RequeueAfter: localization.Spec.Interval.Duration}, nil
	}
	localization.Status.LocalizationDigest = revisionAndDigest.Digest()

	if err := r.Storage.ReconcileArtifact(
		ctx,
		localization,
		revisionAndDigest.Revision(),
		localized,
		fmt.Sprintf("%s.tar.gz", revisionAndDigest.ToFileName()),
		func(artifact *artifactv1.Artifact, s string) error {
			// Archive directory to storage
			if err := r.Storage.Archive(artifact, localized, func(p string, fi os.FileInfo) bool {
				return fi.Name() != "localized.yaml"
			}); err != nil {
				return fmt.Errorf("unable to archive artifact to storage: %w", err)
			}

			localization.Status.ArtifactRef = &v1alpha1.ObjectKey{
				Name:      artifact.Name,
				Namespace: artifact.Namespace,
			}

			return nil
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
	src types.LocalizationSource,
	trgt types.LocalizationTarget,
) (string, error) {
	switch src.GetStrategy().Type {
	case v1alpha1.LocalizationStrategyTypeKustomizePatch:
		return kustomize_patch.Localize(ctx, src, trgt)
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedLocalizationStrategy, src.GetStrategy().Type)
	}
}

func NewMappedRevisionAndDigest(source types.LocalizationSource, target types.LocalizationTarget) MappedRevisionAndDigest {
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

func encodeJSONToString[T any](any T) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	if err := encoder.Encode(any); err != nil {
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

func (r MappedRevisionAndDigest) ToFileName() string {
	return strings.ReplaceAll(r.Digest(), ":", "_")
}

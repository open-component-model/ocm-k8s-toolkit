/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package configuration

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/fluxcd/pkg/runtime/patch"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	configurationclient "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/configuration/client"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/compression"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/index"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	snapshotRegistry "github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	onTargetChange, onConfigChange, err := index.TargetAndConfig[v1alpha1.ConfiguredResource](mgr)
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ConfiguredResource{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// Update when the owned artifact containing the configured data changes
		Owns(&v1alpha1.Snapshot{}).
		// Update when a resource specified as target changes
		Watches(&v1alpha1.Resource{}, onTargetChange).
		Watches(&v1alpha1.LocalizedResource{}, onTargetChange).
		Watches(&v1alpha1.ConfiguredResource{}, onTargetChange).
		// Update when a config coming from a resource changes
		Watches(&v1alpha1.Resource{}, onConfigChange).
		Watches(&v1alpha1.LocalizedResource{}, onConfigChange).
		Watches(&v1alpha1.ConfiguredResource{}, onConfigChange).
		// Update when a config coming from the cluster changes
		Watches(&v1alpha1.ResourceConfig{}, onConfigChange).
		Named("configuredresource").
		Complete(r)
}

// Reconciler reconciles a ConfiguredResource object.
type Reconciler struct {
	*ocm.BaseReconciler
	ConfigClient configurationclient.Client
	Registry     snapshotRegistry.RegistryType
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=configuredresources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=configuredresources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=configuredresources/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=resourceconfigs,verbs=get;list;watch

// +kubebuilder:rbac:groups="",resources=secrets;configmaps;serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)

	configuration := &v1alpha1.ConfiguredResource{}
	if err := r.Get(ctx, req.NamespacedName, configuration); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if configuration.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	if configuration.GetDeletionTimestamp() != nil {
		logger.Info("configuration is being deleted and cannot be used", "name", configuration.Name)

		return ctrl.Result{Requeue: true}, nil
	}

	return r.reconcileWithStatusUpdate(ctx, configuration)
}

func (r *Reconciler) reconcileWithStatusUpdate(ctx context.Context, localization *v1alpha1.ConfiguredResource) (ctrl.Result, error) {
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

//nolint:gocognit // we do not want to cut function at an arbitrary point
func (r *Reconciler) reconcileExists(ctx context.Context, configuration *v1alpha1.ConfiguredResource) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if configuration.Spec.Target.Namespace == "" {
		configuration.Spec.Target.Namespace = configuration.Namespace
	}

	target, err := r.ConfigClient.GetTarget(ctx, configuration.Spec.Target)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.TargetFetchFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to fetch target: %w", err)
	}

	if configuration.Spec.Config.Namespace == "" {
		configuration.Spec.Config.Namespace = configuration.Namespace
	}

	cfg, err := r.ConfigClient.GetConfiguration(ctx, configuration.Spec.Config)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.ConfigFetchFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to fetch cfg: %w", err)
	}

	combinedDigest, revision, _, err := snapshotRegistry.UniqueIDsForSnapshotContentCombination(cfg, target)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.UniqueIDGenerationFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to map combinedDigest from config to target: %w", err)
	}

	// TODO: we cannot use `combinedDigest` to determine a change as the combinedDigest calculation is incorrect
	//   (it takes a k8s object with managed fields that change on every update).

	// Check if a snapshot of the configuration resource already exists and if it holds the same calculated combinedDigest
	// from above
	logger.V(1).Info("verifying configuration", "combinedDigest", combinedDigest, "revision", revision)
	hasValidSnapshot, err := snapshotRegistry.ValidateSnapshotForOwner(
		ctx,
		r.Client,
		configuration,
		combinedDigest,
	)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check if snapshot is valid: %w", err)
	}

	// If no valid snapshot is present (because it never existed or is just not valid), we will configure the target,
	// create a snapshot and return.
	//nolint:nestif // Ignore as it is not that complex.
	if !hasValidSnapshot {
		logger.V(1).Info("configuring", "combinedDigest", combinedDigest, "revision", revision)
		basePath, err := os.MkdirTemp("", "configured-")
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create temporary directory to perform configuration: %w", err)
		}
		defer func() {
			if err := os.RemoveAll(basePath); err != nil {
				logger.Error(err, "failed to remove temporary directory after configuration completed", "path", basePath)
			}
		}()

		configured, err := Configure(ctx, r.ConfigClient, cfg, target, basePath)
		if err != nil {
			status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.ConfigurationFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to configure: %w", err)
		}

		// Create archive from configured directory and gzip it.
		dataTGZ, err := compression.CreateTGZFromPath(configured)
		if err != nil {
			status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.CreateTGZFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to create TGZ from path: %w", err)
		}

		repositoryName, err := snapshotRegistry.CreateRepositoryName(configuration.GetName())
		if err != nil {
			status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.CreateOCIRepositoryNameFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to create repository name: %w", err)
		}

		repository, err := r.Registry.NewRepository(ctx, repositoryName)
		if err != nil {
			status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.ConfigurationFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to configure: %w", err)
		}

		// TODO: Find out which version should be used to tag the OCI artifact.
		//  Things to consider:
		//   - HelmRelease (FluxCD) requires the OCI artifact to have the same tag as the helm chart itself
		//     - But how to get the helm chart version? (User input, parse from content)
		tag := "dummy"
		manifestDigest, err := repository.PushSnapshot(ctx, tag, dataTGZ)
		if err != nil {
			status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.ConfigurationFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to configure: %w", err)
		}

		// We use the combinedDigest calculated above for the blob-info combinedDigest, so we can compare for any changes
		snapshotCR := snapshotRegistry.Create(
			configuration,
			repositoryName,
			manifestDigest.String(),
			&v1alpha1.BlobInfo{
				Digest: combinedDigest,
				Tag:    tag,
				Size:   int64(len(dataTGZ)),
			},
		)

		if _, err = controllerutil.CreateOrUpdate(ctx, r.GetClient(), snapshotCR, func() error {
			if snapshotCR.ObjectMeta.CreationTimestamp.IsZero() {
				if err := controllerutil.SetControllerReference(configuration, snapshotCR, r.GetScheme()); err != nil {
					return fmt.Errorf("failed to set controller reference: %w", err)
				}
			}

			configuration.Status.SnapshotRef = corev1.LocalObjectReference{
				Name: snapshotCR.GetName(),
			}

			return nil
		}); err != nil {
			status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.CreateSnapshotFailedReason, err.Error())

			return ctrl.Result{}, err
		}
	}

	logger.Info("configuration successful", "snapshot", configuration.Status.SnapshotRef)
	status.MarkReady(r.EventRecorder, configuration, "configured successfully")

	return ctrl.Result{RequeueAfter: configuration.Spec.Interval.Duration}, nil
}

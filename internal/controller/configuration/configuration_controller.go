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

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	configurationclient "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/configuration/client"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/compression"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/index"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
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
	Registry     *ociartifact.Registry
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
		if err := ociartifact.DeleteForObject(ctx, r.Registry, configuration); err != nil {
			return ctrl.Result{}, err
		}

		if updated := controllerutil.RemoveFinalizer(configuration, v1alpha1.ArtifactFinalizer); updated {
			if err := r.Update(ctx, configuration); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}

		logger.Info("configuration is being deleted and cannot be used", "name", configuration.Name)

		return ctrl.Result{Requeue: true}, nil
	}

	if updated := controllerutil.AddFinalizer(configuration, v1alpha1.ArtifactFinalizer); updated {
		if err := r.Update(ctx, configuration); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}

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

//nolint:funlen // function length is acceptable
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

	combinedDigest, revision, _, err := ociartifact.UniqueIDsForArtifactContentCombination(cfg, target)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.UniqueIDGenerationFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to map combinedDigest from config to target: %w", err)
	}

	// TODO: we cannot use `combinedDigest` to determine a change as the combinedDigest calculation is incorrect
	//   (it takes a k8s object with managed fields that change on every update).
	// See https://github.com/open-component-model/ocm-k8s-toolkit/issues/150

	// Check if an OCI artifact of the configuration resource already exists and if it holds the same calculated
	// combinedDigest from above. If so, we can skip the configuration process as the target is already configured.
	logger.V(1).Info("verifying configuration", "combinedDigest", combinedDigest, "revision", revision)

	hasValidArtifact := false

	if configuration.GetOCIArtifact() != nil {
		ociRepository, err := r.Registry.NewRepository(ctx, configuration.GetOCIRepository())
		if err != nil {
			status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.CreateOCIRepositoryFailedReason, err.Error())

			return ctrl.Result{}, err
		}

		exists, err := ociRepository.ExistsArtifact(ctx, configuration.GetManifestDigest())
		if err != nil {
			status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.OCIRepositoryExistsFailedReason, err.Error())

			return ctrl.Result{}, err
		}

		if exists {
			hasValidArtifact = combinedDigest == configuration.GetBlobDigest()
		}
	}

	// If no valid OCI artifact is present (because it never existed or is just not valid), we will configure the target,
	// create an OCI artifact and return.
	//nolint:nestif // Ignore as it is not that complex.
	if !hasValidArtifact {
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

		repositoryName, err := ociartifact.CreateRepositoryName(combinedDigest)
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
		// See https://github.com/open-component-model/ocm-k8s-toolkit/issues/151
		tag := "latest"
		manifestDigest, err := repository.PushArtifact(ctx, tag, dataTGZ)
		if err != nil {
			status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.ConfigurationFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to configure: %w", err)
		}

		// Delete previous artifact version, if any.
		err = ociartifact.DeleteIfDigestMismatch(ctx, r.Registry, configuration, manifestDigest)
		if err != nil {
			status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.DeleteOCIArtifactFailedReason, err.Error())

			return ctrl.Result{}, err
		}

		// We use the combinedDigest calculated above for the blob-info combinedDigest, so we can compare for any changes
		configuration.Status.OCIArtifact = &v1alpha1.OCIArtifactInfo{
			Repository: repositoryName,
			Digest:     manifestDigest.String(),
			Blob: &v1alpha1.BlobInfo{
				Digest: combinedDigest,
				Tag:    tag,
				Size:   int64(len(dataTGZ)),
			},
		}
	}

	logger.Info("configuration successful", "OCIArtifact", configuration.GetOCIArtifact())
	status.MarkReady(r.EventRecorder, configuration, "configured successfully")

	return ctrl.Result{RequeueAfter: configuration.Spec.Interval.Duration}, nil
}

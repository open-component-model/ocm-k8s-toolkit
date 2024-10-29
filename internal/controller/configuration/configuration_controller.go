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
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/openfluxcd/controller-manager/storage"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	configurationclient "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/configuration/client"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/index"
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
		// Update when the owned artifact containing the configured data changes
		Owns(&artifactv1.Artifact{}).
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
	*storage.Storage
	ConfigClient configurationclient.Client
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
	log.FromContext(ctx).V(1).Info("reconciling")
	configuration := &v1alpha1.ConfiguredResource{}
	if err := r.Get(ctx, req.NamespacedName, configuration); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if configuration.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	if !configuration.GetDeletionTimestamp().IsZero() {
		// TODO: This is a temporary solution until a artifact-reconciler is written to handle the deletion of artifacts
		if err := ocm.RemoveArtifactForCollectable(ctx, r.Client, r.Storage, configuration); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove artifact: %w", err)
		}

		if removed := controllerutil.RemoveFinalizer(configuration, v1alpha1.ArtifactFinalizer); removed {
			if err := r.Update(ctx, configuration); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}

		return ctrl.Result{}, nil
	}

	if added := controllerutil.AddFinalizer(configuration, v1alpha1.ArtifactFinalizer); added {
		return ctrl.Result{Requeue: true}, r.Update(ctx, configuration)
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

func (r *Reconciler) reconcileExists(ctx context.Context, configuration *v1alpha1.ConfiguredResource) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if err := r.Storage.ReconcileStorage(ctx, configuration); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile storage: %w", err)
	}

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

	digest, revision, filename, err := artifact.UniqueIDsForArtifactContentCombination(cfg, target)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.UniqueIDGenerationFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to map digest from config to target: %w", err)
	}

	logger.V(1).Info("verifying configuration", "digest", digest, "revision", revision)
	hasValidArtifact, err := ocm.ValidateArtifactForCollectable(
		ctx,
		r.Client,
		r.Storage,
		configuration,
		digest,
	)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check if artifact is valid: %w", err)
	}

	var configured string
	if !hasValidArtifact {
		logger.V(1).Info("configuring", "digest", digest, "revision", revision)
		basePath, err := os.MkdirTemp("", "configured-")
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create temporary directory to perform configuration: %w", err)
		}
		defer func() {
			if err := os.RemoveAll(basePath); err != nil {
				logger.Error(err, "failed to remove temporary directory after configuration completed", "path", basePath)
			}
		}()

		if configured, err = Configure(ctx, r.ConfigClient, cfg, target, basePath); err != nil {
			status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.ConfigurationFailedReason, err.Error())

			return ctrl.Result{}, fmt.Errorf("failed to configure: %w", err)
		}
	}

	configuration.Status.Digest = digest

	if err := r.Storage.ReconcileArtifact(
		ctx,
		configuration,
		revision,
		configured,
		filename,
		func(artifact *artifactv1.Artifact, dir string) error {
			if !hasValidArtifact {
				// Archive directory to storage
				if err := r.Storage.Archive(artifact, dir, nil); err != nil {
					return fmt.Errorf("unable to archive artifact to storage: %w", err)
				}
			}

			configuration.Status.ArtifactRef = &v1alpha1.ObjectKey{
				Name:      artifact.Name,
				Namespace: artifact.Namespace,
			}

			return nil
		},
	); err != nil {
		status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.ReconcileArtifactFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to reconcile artifact: %w", err)
	}

	logger.Info("updating")
	logger.Info("configuration successful", "artifact", configuration.Status.ArtifactRef)
	status.MarkReady(r.EventRecorder, configuration, "configured successfully")

	return ctrl.Result{RequeueAfter: configuration.Spec.Interval.Duration}, nil
}

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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openfluxcd/controller-manager/storage"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	configurationclient "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/configuration/client"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/configuration/strategy/mapped"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

const (
	ReasonTargetFetchFailed        = "TargetFetchFailed"
	ReasonConfigFetchFailed        = "ConfigFetchFailed"
	ReasonConfigurationFailed      = "ConfigurationFailed"
	ReasonUniqueIDGenerationFailed = "UniqueIDGenerationFailed"
)

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ConfiguredResource{}).
		Named("configuredresource").
		Complete(r)
}

// Reconciler reconciles a ConfiguredResource object.
type Reconciler struct {
	*ocm.BaseReconciler
	*storage.Storage
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=configuredresources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=configuredresources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=configuredresources/finalizers,verbs=update
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=resourceconfigurations,verbs=get;list;watch

// +kubebuilder:rbac:groups="",resources=secrets;configmaps;serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	configuration := &v1alpha1.ConfiguredResource{}
	if err := r.Get(ctx, req.NamespacedName, configuration); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if configuration.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	if !configuration.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, r.reconcileDeletion(ctx, configuration)
	}

	if added := controllerutil.AddFinalizer(configuration, v1alpha1.ArtifactFinalizer); added {
		return ctrl.Result{Requeue: true}, r.Update(ctx, configuration)
	}

	patchHelper := patch.NewSerialPatcher(configuration, r.Client)

	// Always attempt to patch the object and status after each reconciliation.
	defer func() {
		if statusErr := status.UpdateStatus(ctx, patchHelper, configuration, r.EventRecorder, configuration.Spec.Interval.Duration, err); statusErr != nil {
			err = errors.Join(err, statusErr)
		}
	}()

	return r.reconcileExists(ctx, configuration)
}

func (r *Reconciler) reconcileDeletion(ctx context.Context, configuration *v1alpha1.ConfiguredResource) error {
	artifact, err := ocm.GetAndVerifyArtifactForCollectable(ctx, r, r.Storage, configuration)
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get artifact: %w", err)
	}
	if artifact == nil {
		log.FromContext(ctx).Info("artifact belonging to configuration not found, skipping deletion")

		return nil
	}
	if err := r.Storage.Remove(artifact); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove artifact: %w", err)
		}
	}
	if removed := controllerutil.RemoveFinalizer(configuration, v1alpha1.ArtifactFinalizer); removed {
		if err := r.Update(ctx, configuration); err != nil {
			return fmt.Errorf("failed to remove finalizer: %w", err)
		}
	}

	return nil
}

func (r *Reconciler) reconcileExists(ctx context.Context, configuration *v1alpha1.ConfiguredResource) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if err := r.Storage.ReconcileStorage(ctx, configuration); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile storage: %w", err)
	}

	cfgclnt := configurationclient.NewClientWithLocalStorage(r.Client, r.Storage, r.Scheme)

	if configuration.Spec.Target.Namespace == "" {
		configuration.Spec.Target.Namespace = configuration.Namespace
	}

	target, err := cfgclnt.GetConfigurationTarget(ctx, configuration.Spec.Target)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, configuration, ReasonTargetFetchFailed, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to fetch target: %w", err)
	}

	if configuration.Spec.Source.Namespace == "" {
		configuration.Spec.Source.Namespace = configuration.Namespace
	}

	cfg, err := cfgclnt.GetConfigurationSource(ctx, configuration.Spec.Source)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, configuration, ReasonConfigFetchFailed, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to fetch cfg: %w", err)
	}

	digest, revision, file, err := artifact.UniqueIDsForArtifactContentCombination(cfg, target)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, configuration, ReasonUniqueIDGenerationFailed, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to map digest from config to target: %w", err)
	}

	hasValidArtifact, err := ocm.CollectableHasValidArtifactBasedOnFileNameDigest(
		ctx,
		r.Client,
		r.Storage,
		configuration,
		digest,
	)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check if artifact is valid: %w", err)
	}

	var localized string
	if !hasValidArtifact {
		basePath, err := os.MkdirTemp("", "configured-")
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create temporary directory to perform configuration: %w", err)
		}
		defer func() {
			if err := os.RemoveAll(basePath); err != nil {
				logger.Error(err, "failed to remove temporary directory after configuration completed", "path", basePath)
			}
		}()

		if localized, err = mapped.Configure(ctx, cfgclnt, cfg, target, basePath); err != nil {
			status.MarkNotReady(r.EventRecorder, configuration, ReasonConfigurationFailed, err.Error())
			logger.Error(err, "failed to configure, retrying later", "interval", configuration.Spec.Interval.Duration)

			return ctrl.Result{RequeueAfter: configuration.Spec.Interval.Duration}, nil
		}
	}

	configuration.Status.Digest = digest

	if err := r.Storage.ReconcileArtifact(
		ctx,
		configuration,
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

			configuration.Status.ArtifactRef = &v1alpha1.ObjectKey{
				Name:      artifact.Name,
				Namespace: artifact.Namespace,
			}

			return os.RemoveAll(dir)
		},
	); err != nil {
		status.MarkNotReady(r.EventRecorder, configuration, v1alpha1.ReconcileArtifactFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to reconcile artifact: %w", err)
	}

	logger.Info("configuration successful", "artifact", configuration.Status.ArtifactRef)
	status.MarkReady(r.EventRecorder, configuration, "localized successfully")

	return ctrl.Result{RequeueAfter: configuration.Spec.Interval.Duration}, nil
}

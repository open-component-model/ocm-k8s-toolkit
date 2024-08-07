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

package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/fluxcd/pkg/runtime/patch"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ocmctx "ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/compdesc"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/ocm"
)

// ComponentReconciler reconciles a Component object.
type ComponentReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Storage   *storage.Storage
	OCMClient ocm.Contract
}

type Components struct {
	List []*compdesc.ComponentDescriptor `json:"components"`
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=components,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=components/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=components/finalizers,verbs=update

// +kubebuilder:rbac:groups=openfluxcd.mandelsoft.org,resources=artifacts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openfluxcd.mandelsoft.org,resources=artifacts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openfluxcd.mandelsoft.org,resources=artifacts/finalizers,verbs=update

// +kubebuilder:rbac:groups="",resources=secrets;configmaps;serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile the component object.
func (r *ComponentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	logger := log.FromContext(ctx).WithName("component-controller")

	obj := &deliveryv1alpha1.Component{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if obj.GetDeletionTimestamp() != nil {
		logger.Info("deleting component", "name", obj.Name)

		return ctrl.Result{}, nil
	}

	if obj.Spec.Suspend {
		logger.Info("component is suspended, skipping reconciliation")

		return ctrl.Result{}, nil
	}

	patchHelper := patch.NewSerialPatcher(obj, r.Client)

	// Always attempt to patch the object and status after each reconciliation.
	defer func() {
		if perr := patchHelper.Patch(ctx, obj); perr != nil {
			retErr = errors.Join(retErr, perr)
		}
	}()

	repositoryObject := &deliveryv1alpha1.OCMRepository{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: obj.Spec.RepositoryRef.Namespace,
		Name:      obj.Spec.RepositoryRef.Name,
	}, repositoryObject); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get repository: %w", err)
	}

	octx, err := r.OCMClient.CreateAuthenticatedOCMContext(ctx, repositoryObject)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create authenticated OCM context: %w", err)
	}

	// reconcile the version before calling reconcile func
	update, version, err := r.checkVersion(ctx, octx, obj, repositoryObject.Spec.RepositorySpec.Raw)
	if err != nil {
		// The component might not be there yet. We don't fail but keep polling instead.
		return ctrl.Result{
			RequeueAfter: obj.GetRequeueAfter(),
		}, nil
	}

	if !update {
		logger.Info("reconciliation skipped, no update needed")
		return ctrl.Result{
			RequeueAfter: obj.GetRequeueAfter(),
		}, nil
	}

	logger.Info("start reconciling new version", "version", version)

	cv, err := r.OCMClient.GetComponentVersion(ctx, octx, obj.Spec.Component, version, repositoryObject.Spec.RepositorySpec.Raw)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to retrieve component: %w", err)
	}
	defer cv.Close()

	desc := cv.GetDescriptor()
	descriptors := []*compdesc.ComponentDescriptor{desc}
	if err := r.traversReferences(ctx, octx, &descriptors, desc.References, repositoryObject.Spec.RepositorySpec.Raw); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to travers references: %w", err)
	}

	list := &Components{
		List: descriptors,
	}

	// Reconcile the storage to create the main location and prepare the server.
	if err := r.Storage.ReconcileStorage(ctx, obj); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile storage: %w", err)
	}

	// Create temp working dir
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("%s-%s-%s-", obj.Kind, obj.Namespace, obj.Name))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create temporary working directory: %w", err)
	}
	defer func() {
		if err = os.RemoveAll(tmpDir); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to remove temporary working directory")
		}
	}()

	content, err := yaml.Marshal(list)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to marshal content: %w", err)
	}

	const perm = 0o655
	if err := os.WriteFile(filepath.Join(tmpDir, "component-descriptor.yaml"), content, perm); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to write file: %w", err)
	}

	revision := r.normalizeComponentVersionName(cv.GetName()) + "-" + cv.GetVersion()
	if err := r.Storage.ReconcileArtifact(
		ctx,
		obj,
		revision,
		tmpDir,
		revision+".tar.gz",
		func(art *artifactv1.Artifact, _ string) error {
			// Archive directory to storage
			if err := r.Storage.Archive(art, tmpDir, nil); err != nil {
				return fmt.Errorf("unable to archive artifact to storage: %w", err)
			}

			obj.Status.ArtifactRef = v1.LocalObjectReference{
				Name: art.Name,
			}

			return nil
		},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile artifact: %w", err)
	}

	// Update status
	obj.Status.Component = deliveryv1alpha1.ComponentInfo{
		RepositorySpec: repositoryObject.Spec.RepositorySpec,
		Component:      obj.Spec.Component,
		Version:        cv.GetVersion(),
	}

	// Return done.

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Component{}).
		Complete(r)
}

func (r *ComponentReconciler) normalizeComponentVersionName(name string) string {
	return strings.ReplaceAll(name, "/", "-")
}

func (r *ComponentReconciler) checkVersion(
	ctx context.Context,
	octx ocmctx.Context,
	obj *deliveryv1alpha1.Component,
	repoConfig []byte,
) (bool, string, error) {
	logger := log.FromContext(ctx).WithName("version-reconcile")

	latest, err := r.OCMClient.GetLatestValidComponentVersion(ctx, octx, obj, repoConfig)
	if err != nil {
		return false, "", fmt.Errorf("failed to get latest component version: %w", err)
	}
	logger.V(deliveryv1alpha1.LevelDebug).Info("got latest version of component", "version", latest)

	latestSemver, err := semver.NewVersion(latest)
	if err != nil {
		return false, "", fmt.Errorf("failed to parse latest version: %w", err)
	}

	reconciledVersion := "0.0.0"
	if obj.Status.Component.Version != "" {
		reconciledVersion = obj.Status.Component.Version
	}
	current, err := semver.NewVersion(reconciledVersion)
	if err != nil {
		return false, "", fmt.Errorf("failed to parse reconciled version: %w", err)
	}
	logger.V(deliveryv1alpha1.LevelDebug).Info("current reconciled version is", "reconciled", current.String())

	if latestSemver.Equal(current) || current.GreaterThan(latestSemver) {
		logger.V(deliveryv1alpha1.LevelDebug).Info("Reconciled version equal to or greater than newest available version", "version", latestSemver)

		return false, latest, nil
	}

	return true, latest, nil
}

func (r *ComponentReconciler) traversReferences(ctx context.Context, octx ocmctx.Context, list *[]*compdesc.ComponentDescriptor, references compdesc.References, repoConfig []byte) error {
	logger := log.FromContext(ctx).WithName("travers-references")
	for _, ref := range references {
		logger.Info("fetching embedded component", "component", ref.ComponentName, "version", ref.Version)

		cv, err := r.OCMClient.GetComponentVersion(ctx, octx, ref.ComponentName, ref.Version, repoConfig)
		if err != nil {
			return err
		}

		desc := cv.GetDescriptor()
		*list = append(*list, desc)

		if len(desc.References) > 0 {
			if err := r.traversReferences(ctx, octx, list, desc.References, repoConfig); err != nil {
				return err
			}
		}
	}

	return nil
}

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

	"github.com/openfluxcd/controller-manager/storage"

	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/helpers"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/rerror"

	"github.com/mandelsoft/goutils/general"
	"github.com/mandelsoft/goutils/sliceutils"
	"ocm.software/ocm/api/datacontext"
	"ocm.software/ocm/api/ocm/resolvers"

	"github.com/Masterminds/semver/v3"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/status"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ocmctx "ocm.software/ocm/api/ocm"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/yaml"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/ocm"
)

const (
	Realm = "component-controller"
)

// ComponentReconciler reconciles a Component object.
type ComponentReconciler struct {
	*helpers.OCMK8SBaseReconciler
	Storage *storage.Storage
}

var _ helpers.OCMK8SReconciler = (*ComponentReconciler)(nil)

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Component{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
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
	component := &deliveryv1alpha1.Component{}
	if err := r.Get(ctx, req.NamespacedName, component); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, component)
}

func (r *ComponentReconciler) reconcileExists(ctx context.Context, component *deliveryv1alpha1.Component) (_ ctrl.Result, retErr error) {
	logger := log.FromContext(ctx)
	if component.GetDeletionTimestamp() != nil {
		logger.Info("deleting component", "name", component.Name)

		return ctrl.Result{}, nil
	}

	if component.Spec.Suspend {
		logger.Info("component is suspended, skipping reconciliation")

		return ctrl.Result{}, nil
	}

	return r.reconcilePrepare(ctx, component)
}

func (r *ComponentReconciler) reconcilePrepare(ctx context.Context, component *deliveryv1alpha1.Component) (_ ctrl.Result, retErr error) {
	logger := log.FromContext(ctx)

	patchHelper := patch.NewSerialPatcher(component, r.Client)

	// Always attempt to patch the object and status after each reconciliation.
	defer func() {
		if perr := status.UpdateStatus(ctx, patchHelper, component, r.EventRecorder, component.GetRequeueAfter(), retErr); perr != nil {
			retErr = errors.Join(retErr, perr)
		}
	}()

	repo := &deliveryv1alpha1.OCMRepository{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: component.Spec.RepositoryRef.Namespace,
		Name:      component.Spec.RepositoryRef.Name,
	}, repo); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get repository: %w", err)
	}

	if !conditions.IsReady(repo) {
		logger.Info("repository is not ready", "name", component.Spec.RepositoryRef.Name)
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.RepositoryIsNotReadyReason, "Repository is not ready yet")

		return ctrl.Result{RequeueAfter: component.GetRequeueAfter()}, nil
	}

	return r.reconcile(ctx, component, repo)
}

func (r *ComponentReconciler) reconcile(ctx context.Context, component *deliveryv1alpha1.Component, repository *deliveryv1alpha1.OCMRepository) (_ ctrl.Result, retErr error) {
	// DefaultContext is essentially the same as the extended context created here. The difference is, if we
	// register a new type at an extension point (e.g. a new access type), it's only registered at this exact context
	// instance and not at the global default context variable.
	octx := ocmctx.New(datacontext.MODE_EXTENDED)
	defer func() {
		retErr = errors.Join(retErr, octx.Finalize())
	}()
	session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

	err := helpers.ConfigureOCMContext(ctx, r, octx, component, repository)
	if err != nil {
		return ctrl.Result{}, err
	}

	repo, err := session.LookupRepositoryForConfig(octx, repository.Spec.RepositorySpec.Raw)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.RepositorySpecInvalidReason, "RepositorySpec is invalid")

		return ctrl.Result{}, fmt.Errorf("invalid repository spec: %w", err)
	}

	c, err := session.LookupComponent(repo, component.Spec.Component)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.GetComponentFailedReason, "Component not found in repository")

		return ctrl.Result{
			// component just might be not there (or rather created) yet, requeue without backoff to avoid long wait
			// times
			RequeueAfter: component.GetRequeueAfter(),
		}, nil
	}

	version, rerr := r.determineEffectiveVersion(ctx, component, session, repo, c)
	if rerr != nil {
		if rerr.Retryable() {
			return ctrl.Result{
				RequeueAfter: component.GetRequeueAfter(),
			}, err
		}

		return ctrl.Result{}, err
	}

	cv, err := session.LookupComponentVersion(repo, c.GetName(), version)
	if err != nil {
		// this version has to exist (since it was found in GetLatestVersion) and therefore, this is most likely a
		// static error where requeueing does not make sense
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get component version: %w", err)
	}

	descriptors, rerr := r.verifyComponentVersionAndListDescriptors(ctx, octx, component, cv)
	if rerr != nil {
		if rerr.Retryable() {
			return ctrl.Result{
				RequeueAfter: component.GetRequeueAfter(),
			}, err
		}

		return ctrl.Result{}, err
	}

	if err := r.Storage.ReconcileStorage(ctx, component); err != nil {
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.StorageReconcileFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to reconcileComponent storage: %w", err)
	}

	rerr = r.createArtifactForDescriptors(ctx, octx, component, cv, descriptors)
	if rerr != nil {
		if rerr.Retryable() {
			return ctrl.Result{
				RequeueAfter: component.GetRequeueAfter(),
			}, err
		}

		return ctrl.Result{}, err
	}

	// Update status
	component.Status.Component = deliveryv1alpha1.ComponentInfo{
		RepositorySpec: repository.Spec.RepositorySpec,
		Component:      component.Spec.Component,
		Version:        version,
	}
	status.MarkReady(r.EventRecorder, component, "Applied version %s", version)

	return ctrl.Result{}, nil
}

func (r *ComponentReconciler) determineEffectiveVersion(ctx context.Context, component *deliveryv1alpha1.Component,
	session ocmctx.Session, repo ocmctx.Repository, c ocmctx.ComponentAccess,
) (string, rerror.ReconcileError) {
	versions, err := c.ListVersions()
	if err != nil || len(versions) == 0 {
		// for most repository implementations (especially oci), there is no way to check whether a component exists but
		// trying to list all versions
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.GetComponentFailedReason, "Component not found in repository")

		return "", rerror.AsNonRetryableError(fmt.Errorf("component %s not found in repository", c.GetName()))
	}
	filter, err := ocm.RegexpFilter(component.Spec.SemverFilter)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.CheckVersionFailedReason, err.Error())

		return "", rerror.AsNonRetryableError(fmt.Errorf("failed to parse regexp filter: %w", err))
	}
	latestSemver, err := ocm.GetLatestValidVersion(ctx, versions, component.Spec.Semver, filter)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.CheckVersionFailedReason, err.Error())

		return "", rerror.AsNonRetryableError(fmt.Errorf("failed to check latest version: %w", err))
	}
	latestcv, err := session.LookupComponentVersion(repo, c.GetName(), latestSemver.String())
	if err != nil {
		// this version has to exist (since it was found in GetLatestVersion) and therefore, this is most likely a
		// static error where requeueing does not make sense
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.GetComponentVersionFailedReason, err.Error())

		return "", rerror.AsNonRetryableError(fmt.Errorf("failed to get component version: %w", err))
	}

	reconciledVersion := general.OptionalDefaulted(component.Status.Component.Version, "0.0.0")
	currentSemver, err := semver.NewVersion(reconciledVersion)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.CheckVersionFailedReason, err.Error())

		return "", rerror.AsNonRetryableError(fmt.Errorf("failed to check reconciled version: %w", err))
	}

	if latestSemver.LessThan(currentSemver) && !component.Spec.EnforceDowngradability {
		downgradable := false
		if reconciledVersion != "0.0.0" {
			reconciledcv, err := session.LookupComponentVersion(repo, component.GetName(), reconciledVersion)
			if err != nil {
				status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.GetComponentVersionFailedReason, err.Error())

				return "", rerror.AsNonRetryableError(fmt.Errorf("failed to get reconciled component version to check"+
					"downgradability: %w", err))
			}
			downgradable, err = ocm.IsDowngradable(ctx, reconciledcv, latestcv)
			if err != nil {
				status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.CheckVersionFailedReason, err.Error())

				return "", rerror.AsNonRetryableError(fmt.Errorf("failed to check downgradability: %w", err))
			}
		}

		if !downgradable {
			status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.CheckVersionFailedReason,
				fmt.Sprintf("component version cannot be downgraded from version %s to version %s",
					currentSemver.String(), latestSemver.String()))
			// keep requeueing, a greater component version could be published
			// semver constraint may even describe older versions and non-existing newer versions, so you have to check
			// for potential newer versions (current is downgradable to: > 1.0.3, latest is: < 1.1.0, but version 1.0.4
			// does not exist yet, but will be created)
			return "", rerror.AsRetryableError(fmt.Errorf("component version cannot be downgraded from version %s "+
				"to version %s", currentSemver.String(), latestSemver.String()))
		}
	}

	return latestSemver.String(), nil
}

func (r *ComponentReconciler) verifyComponentVersionAndListDescriptors(ctx context.Context, octx ocmctx.Context,
	component *deliveryv1alpha1.Component, cv ocmctx.ComponentVersionAccess,
) (*ocm.Descriptors, rerror.ReconcileError) {
	logger := log.FromContext(ctx)
	descriptors, err := ocm.VerifyComponentVersion(ctx, cv, sliceutils.Transform(component.Spec.Verify, func(verify deliveryv1alpha1.Verification) string {
		return verify.Signature
	}))
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.VerificationFailedReason, err.Error())

		return nil, rerror.AsNonRetryableError(fmt.Errorf("failed to verify component: %w", err))
	}
	logger.Info("component successfully verified", "version", cv.GetVersion(), "component", cv.GetName())

	// if the component descriptors were not collected during signature validation, collect them now
	if descriptors == nil || len(descriptors.List) == 0 {
		descriptors, err = ocm.ListComponentDescriptors(ctx, cv, resolvers.NewCompoundResolver(cv.Repository(), octx.GetResolver()))
		if err != nil {
			status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.ListComponentDescriptorsFailedReason, err.Error())

			return nil, rerror.AsRetryableError(fmt.Errorf("failed to list component descriptors: %w", err))
		}
	}

	return descriptors, nil
}

func (r *ComponentReconciler) createArtifactForDescriptors(ctx context.Context, octx ocmctx.Context,
	component *deliveryv1alpha1.Component, cv ocmctx.ComponentVersionAccess, descriptors *ocm.Descriptors,
) rerror.ReconcileError {
	logger := log.FromContext(ctx)

	// Create temp working dir
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("%s-%s-%s-", component.Kind, component.Namespace, component.Name))
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.TemporaryFolderCreationFailedReason, err.Error())

		return rerror.AsNonRetryableError(fmt.Errorf("failed to create temporary working directory: %w", err))
	}
	octx.Finalizer().With(func() error {
		if err = os.RemoveAll(tmpDir); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to remove temporary working directory")
		}

		return nil
	})

	content, err := yaml.Marshal(descriptors)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.MarshallingComponentDescriptorsFailedReason, err.Error())

		return rerror.AsNonRetryableError(fmt.Errorf("failed to marshal content: %w", err))
	}

	const perm = 0o655
	if err := os.WriteFile(filepath.Join(tmpDir, deliveryv1alpha1.OCMComponentDescriptorList), content, perm); err != nil {
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.WritingComponentFileFailedReason, err.Error())

		return rerror.AsNonRetryableError(fmt.Errorf("failed to write file: %w", err))
	}

	// TODO: why do we have to normalize at all? is it important that the directory structure is flat

	revision := r.normalizeComponentVersionName(cv.GetName()) + "-" + cv.GetVersion()
	if err := r.Storage.ReconcileArtifact(
		ctx,
		component,
		revision,
		tmpDir,
		revision+".tar.gz",
		func(art *artifactv1.Artifact, _ string) error {
			// Archive directory to storage
			if err := r.Storage.Archive(art, tmpDir, nil); err != nil {
				return fmt.Errorf("unable to archive artifact to storage: %w", err)
			}

			component.Status.ArtifactRef = corev1.LocalObjectReference{
				Name: art.Name,
			}

			return nil
		},
	); err != nil {
		status.MarkNotReady(r.EventRecorder, component, deliveryv1alpha1.ReconcileArtifactFailedReason, err.Error())

		return rerror.AsNonRetryableError(fmt.Errorf("failed to reconcileComponent artifact: %w", err))
	}

	logger.Info("successfully reconciled component", "name", component.Name)

	return nil
}

// TODO: github.com/my-component and github.com/my/component would collide
//	shouldn't do this differently, e.g. replace - with -- and / with -
//	Why do we have to flatten this in the first place?

func (r *ComponentReconciler) normalizeComponentVersionName(name string) string {
	return strings.ReplaceAll(name, "/", "-")
}

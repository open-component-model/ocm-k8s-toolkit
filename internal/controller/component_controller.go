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
	"github.com/mandelsoft/goutils/general"
	"github.com/mandelsoft/goutils/sliceutils"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/utils"
	"ocm.software/ocm/api/datacontext"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/status"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kuberecorder "k8s.io/client-go/tools/record"
	ocmctx "ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/compdesc"
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
	client.Client
	Scheme *runtime.Scheme
	kuberecorder.EventRecorder

	Storage *storage.Storage
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Component{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
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
	// TODO: Discuss whether it would make sense to initialize the ocm context here and call a defer close. This way, we
	//  can simply add stuff that we'd want to be closed to the close of the context.
	//  FYI: DefaultContext is essentially the same as the extended context created here. The difference is, if we
	//  register a new type at an extension point (e.g. a new access type), it's only registered at this exact context
	//  instance and not at the global default context variable.
	octx := ocmctx.New(datacontext.MODE_EXTENDED)
	defer func() {
		retErr = errors.Join(retErr, octx.Finalize())
	}()
	octx.LoggingContext().SetBaseLogger(log.FromContext(ctx), true)
	logger := octx.LoggingContext().Logger(Realm).WithName("component-controller")

	session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

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
		if perr := status.UpdateStatus(ctx, patchHelper, obj, r.EventRecorder, obj.GetRequeueAfter(), retErr); perr != nil {
			retErr = errors.Join(retErr, perr)
		}
	}()

	repoObj := &deliveryv1alpha1.OCMRepository{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: obj.Spec.RepositoryRef.Namespace,
		Name:      obj.Spec.RepositoryRef.Name,
	}, repoObj); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get repository: %w", err)
	}

	if !conditions.IsReady(repoObj) {
		logger.Info("repository is not ready", "name", obj.Spec.RepositoryRef.Name)
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.RepositoryIsNotReadyReason, "Repository is not ready yet")

		return ctrl.Result{RequeueAfter: obj.GetRequeueAfter()}, nil
	}

	// Configure ocm context
	secrets, err := utils.GetSecrets(ctx, r.Client, append(utils.GetEffectiveSecretRefs(ctx, obj, repoObj),
		sliceutils.Transform(
			sliceutils.Filter(obj.Spec.Verify, func(v deliveryv1alpha1.Verification) bool {
				return v.Value == "" && v.SecretRef != ""
			}),
			func(v deliveryv1alpha1.Verification) client.ObjectKey {
				return client.ObjectKey{
					Namespace: obj.Namespace,
					Name:      v.SecretRef,
				}
			})...))
	if err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.SecretFetchFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to get secrets: %w", err)
	}

	configs, err := utils.GetConfigMaps(ctx, r.Client, utils.GetEffectiveConfigRefs(ctx, obj, repoObj))
	if err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.ConfigFetchFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to get configmaps: %w", err)
	}

	set := utils.GetEffectiveConfigSet(ctx, obj, repoObj)

	err = ocm.ConfigureContext(octx, obj, secrets, configs, set)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.ConfigureContextFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to get configmaps: %w", err)
	}

	repo, err := session.LookupRepositoryForConfig(octx, repoObj.Spec.RepositorySpec.Raw)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.RepositorySpecInvalidReason, "RepositorySpec is invalid")

		return ctrl.Result{}, fmt.Errorf("invalid repository spec: %w", err)
	}

	component, err := session.LookupComponent(repo, obj.Spec.Component)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.GetComponentFailedReason, "Component not found in repository")

		return ctrl.Result{
			// component just might be not there (or rather created) yet, requeue without backoff to avoid long wait
			// times
			RequeueAfter: obj.GetRequeueAfter(),
		}, nil
	}

	// check version before calling reconcile func
	filter, err := ocm.RegexpFilter(obj.Spec.SemverFilter)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.CheckVersionFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to parse regexp filter: %w", err)
	}
	latestSemver, err := ocm.GetLatestValidVersion(component, obj.Spec.Semver, filter)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.CheckVersionFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to check latest version: %w", err)
	}
	latestcv, err := session.LookupComponentVersion(repo, component.GetName(), latestSemver.String())
	if err != nil {
		// this version has to exist (since it was found in GetLatestVersion) and therefore, this is most likely a
		// static error where requeueing does not make sense
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.GetComponentVersionFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to get component version: %w", err)
	}

	reconciledVersion := general.OptionalDefaulted(obj.Status.Component.Version, "0.0.0")
	currentSemver, err := semver.NewVersion(reconciledVersion)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.CheckVersionFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to check reconciled version: %w", err)
	}

	if latestSemver.LessThan(currentSemver) && !obj.Spec.EnforceDowngradability {
		downgradable := false
		if reconciledVersion != "0.0.0" {
			reconciledcv, err := session.LookupComponentVersion(repo, component.GetName(), reconciledVersion)
			if err != nil {
				// this version has to exist (since it was found in GetLatestVersion) and therefore, this is most likely a
				// static error where requeueing does not make sense
				status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.GetComponentVersionFailedReason, err.Error())
				return ctrl.Result{}, fmt.Errorf("failed to get reconciled component version to check"+
					"downgradability: %w", err)
			}
			downgradable, err = ocm.IsDowngradable(ctx, reconciledcv, latestcv)
			if err != nil {
				status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.CheckVersionFailedReason, err.Error())
				return ctrl.Result{}, fmt.Errorf("failed to check downgradability: %w", err)
			}
		}

		if !downgradable {
			status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.CheckVersionFailedReason,
				fmt.Sprintf("component version cannot be downgraded from version %s to version: %s",
					currentSemver.String(), latestSemver.String()))
			// keep requeueing, a greater component version could be published
			return ctrl.Result{
				RequeueAfter: obj.GetRequeueAfter(),
			}, nil
		}
	}
	version := latestSemver.String()

	logger.Info("start reconciling new version", "version", version)
	if err := r.reconcile(ctx, octx, obj, latestcv); err != nil {
		// We don't MarkAs* here because the `reconcile` function marks the specific status.
		return ctrl.Result{}, err
	}

	// Update status
	obj.Status.Component = deliveryv1alpha1.ComponentInfo{
		RepositorySpec: repoObj.Spec.RepositorySpec,
		Component:      obj.Spec.Component,
		Version:        version,
	}

	status.MarkReady(r.EventRecorder, obj, "Applied version %s", version)

	return ctrl.Result{}, nil
}

func (r *ComponentReconciler) normalizeComponentVersionName(name string) string {
	return strings.ReplaceAll(name, "/", "-")
}

// reconcile perform the rest of the reconciliation routine after the component has been verified and checked for new
// versions.
func (r *ComponentReconciler) reconcile(
	ctx context.Context,
	octx ocmctx.Context,
	obj *deliveryv1alpha1.Component,
	cv ocmctx.ComponentVersionAccess,
) error {
	logger := octx.Logger(Realm).WithName("reconcile")

	logger.Info("reconciling component", "name", obj.Name)

	descriptors, err := ocm.VerifyComponentVersion(cv, sliceutils.Transform(obj.Spec.Verify, func(verify deliveryv1alpha1.Verification) string {
		return verify.Signature
	}))
	if err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.VerificationFailedReason, err.Error())

		return fmt.Errorf("failed to verify component: %w", err)
	}
	logger.Info("component successfully verified", "version", cv.GetVersion(), "component", cv.GetName())

	// if the component descriptors were not collected during signature validation, collect them now
	if len(descriptors) == 0 {
		descriptors, err = ocm.ListComponentDescriptors(cv, ocmctx.NewCompoundResolver(cv.Repository(), octx.GetResolver()))
		if err != nil {
			status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.ListComponentDescriptorsFailedReason, err.Error())

			return fmt.Errorf("failed to reconcile storage: %w", err)
		}
	}

	list := &Components{
		List: descriptors,
	}

	// Reconcile the storage to create the main location and prepare the server.
	if err := r.Storage.ReconcileStorage(ctx, obj); err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.StorageReconcileFailedReason, err.Error())

		return fmt.Errorf("failed to reconcile storage: %w", err)
	}

	// Create temp working dir
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("%s-%s-%s-", obj.Kind, obj.Namespace, obj.Name))
	if err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.TemporaryFolderCreationFailedReason, err.Error())

		return fmt.Errorf("failed to create temporary working directory: %w", err)
	}
	defer func() {
		if err = os.RemoveAll(tmpDir); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to remove temporary working directory")
		}
	}()

	content, err := yaml.Marshal(list)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.MarshallingComponentDescriptorsFailedReason, err.Error())

		return fmt.Errorf("failed to marshal content: %w", err)
	}

	const perm = 0o655
	if err := os.WriteFile(filepath.Join(tmpDir, "component-descriptor.yaml"), content, perm); err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.WritingComponentFileFailedReason, err.Error())

		return fmt.Errorf("failed to write file: %w", err)
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

			obj.Status.ArtifactRef = corev1.LocalObjectReference{
				Name: art.Name,
			}

			return nil
		},
	); err != nil {
		status.MarkNotReady(r.EventRecorder, obj, deliveryv1alpha1.ReconcileArtifactFailedReason, err.Error())

		return fmt.Errorf("failed to reconcile artifact: %w", err)
	}

	logger.Info("successfully reconciled component", "name", obj.Name)

	return nil
}

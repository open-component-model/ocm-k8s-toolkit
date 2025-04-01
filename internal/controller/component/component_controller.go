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

package component

import (
	"context"
	"errors"
	"fmt"

	"github.com/Masterminds/semver/v3"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/mandelsoft/goutils/sliceutils"
	"github.com/opencontainers/go-digest"
	"k8s.io/apimachinery/pkg/types"
	"ocm.software/ocm/api/datacontext"
	"ocm.software/ocm/api/ocm/resolvers"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	ocmctx "ocm.software/ocm/api/ocm"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

// Reconciler reconciles a Component object.
type Reconciler struct {
	*ocm.BaseReconciler
	Registry *ociartifact.Registry
}

var _ ocm.Reconciler = (*Reconciler)(nil)

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create index for ocmrepository reference name from components to make sure to reconcile, when the base ocm-
	// repository changes.
	const fieldName = "spec.repositoryRef.name"
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.Component{}, fieldName, func(obj client.Object) []string {
		component, ok := obj.(*v1alpha1.Component)
		if !ok {
			return nil
		}

		return []string{component.Spec.RepositoryRef.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Component{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&v1alpha1.OCMRepository{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				ocmRepository, ok := obj.(*v1alpha1.OCMRepository)
				if !ok {
					return []reconcile.Request{}
				}

				// Get list of components that reference the ocmrepository
				list := &v1alpha1.ComponentList{}
				if err := r.List(ctx, list, client.MatchingFields{fieldName: ocmRepository.GetName()}); err != nil {
					return []reconcile.Request{}
				}

				// For every component that references the ocmrepository create a reconciliation request for that
				// component
				requests := make([]reconcile.Request, 0, len(list.Items))
				for _, component := range list.Items {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Namespace: component.GetNamespace(),
							Name:      component.GetName(),
						},
					})
				}

				return requests
			})).
		Complete(r)
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=components,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=components/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=components/finalizers,verbs=update

// +kubebuilder:rbac:groups="",resources=secrets;configmaps;serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile the component object.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	log.FromContext(ctx).Info("reconciling component", "name", req.Name)
	component := &v1alpha1.Component{}
	if err := r.Get(ctx, req.NamespacedName, component); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileWithStatusUpdate(ctx, component)
}

func (r *Reconciler) reconcileWithStatusUpdate(ctx context.Context, component *v1alpha1.Component) (ctrl.Result, error) {
	patchHelper := patch.NewSerialPatcher(component, r.Client)

	result, err := r.reconcileExists(ctx, component)

	// Always attempt to patch the object and status after each reconciliation.
	err = errors.Join(err, status.UpdateStatus(ctx, patchHelper, component, r.EventRecorder, component.GetRequeueAfter(), err))
	if err != nil {
		return ctrl.Result{}, err
	}

	return result, nil
}

func (r *Reconciler) reconcileExists(ctx context.Context, component *v1alpha1.Component) (_ ctrl.Result, retErr error) {
	logger := log.FromContext(ctx)

	if !component.GetDeletionTimestamp().IsZero() {
		if err := ociartifact.DeleteForObject(ctx, r.Registry, component); err != nil {
			return ctrl.Result{}, err
		}

		if updated := controllerutil.RemoveFinalizer(component, v1alpha1.ArtifactFinalizer); updated {
			if err := r.Update(ctx, component); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}

			return ctrl.Result{}, nil
		}

		logger.Info("component is being deleted and still has existing finalizers", "name", component.GetName())

		return ctrl.Result{Requeue: true}, nil
	}

	if updated := controllerutil.AddFinalizer(component, v1alpha1.ArtifactFinalizer); updated {
		if err := r.Update(ctx, component); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}

		return ctrl.Result{Requeue: true}, nil
	}

	if component.Spec.Suspend {
		logger.Info("component is suspended, skipping reconciliation")

		return ctrl.Result{}, nil
	}

	return r.reconcile(ctx, component)
}

func (r *Reconciler) reconcile(ctx context.Context, component *v1alpha1.Component) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	repo := &v1alpha1.OCMRepository{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: component.Spec.RepositoryRef.Namespace,
		Name:      component.Spec.RepositoryRef.Name,
	}, repo); err != nil {
		logger.Info("failed to get repository")

		return ctrl.Result{}, fmt.Errorf("failed to get repository: %w", err)
	}

	if !repo.DeletionTimestamp.IsZero() {
		err := errors.New("repository is being deleted, please do not use it")
		logger.Error(err, "repository is being deleted, please do not use it", "name", component.Spec.RepositoryRef.Name)

		return ctrl.Result{}, nil
	}

	// Note: Marking the component as not ready, when the ocmrepository is not ready is not completely valid. As the
	// component was potentially ready, then the ocmrepository changed, but that does not necessarily mean that the
	// component is not ready as well.
	// However, as the component is hard-dependant on the ocmrepository, we decided to mark it not ready as well.
	if !conditions.IsReady(repo) {
		logger.Info("repository is not ready", "name", component.Spec.RepositoryRef.Name)
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.RepositoryIsNotReadyReason, "repository is not ready yet")

		return ctrl.Result{}, nil
	}

	return r.reconcileOCM(ctx, component, repo)
}

func (r *Reconciler) reconcileOCM(ctx context.Context, component *v1alpha1.Component, repository *v1alpha1.OCMRepository) (ctrl.Result, error) {
	// DefaultContext is essentially the same as the extended context created here. The difference is, if we
	// register a new type at an extension point (e.g. a new access type), it's only registered at this exact context
	// instance and not at the global default context variable.
	octx := ocmctx.New(datacontext.MODE_EXTENDED)

	result, err := r.reconcileComponent(ctx, octx, component, repository)

	// Always finalize ocm context after reconciliation
	err = errors.Join(err, octx.Finalize())
	if err != nil {
		// this should be retryable, as it is difficult to foresee whether
		// another error condition might lead to problems closing the ocm
		// context
		return ctrl.Result{}, err
	}

	return result, nil
}

//nolint:funlen // we do not want to cut function at an arbitrary point
func (r *Reconciler) reconcileComponent(ctx context.Context, octx ocmctx.Context, component *v1alpha1.Component, repository *v1alpha1.OCMRepository) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

	configs, err := ocm.GetEffectiveConfig(ctx, r.GetClient(), component)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), component, v1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	verifications, err := ocm.GetVerifications(ctx, r.GetClient(), component)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), component, v1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	err = ocm.ConfigureContext(ctx, octx, r.GetClient(), configs, verifications)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), component, v1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	spec, err := octx.RepositorySpecForConfig(repository.Spec.RepositorySpec.Raw, nil)
	if err != nil {
		logger.Error(err, "failed to parse repository spec")
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.RepositorySpecInvalidReason, "RepositorySpec is invalid")

		return ctrl.Result{}, err
	}

	repo, err := session.LookupRepository(octx, spec)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.RepositorySpecInvalidReason, "RepositorySpec is invalid")

		return ctrl.Result{}, fmt.Errorf("invalid repository spec: %w", err)
	}

	c, err := session.LookupComponent(repo, component.Spec.Component)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.GetComponentFailedReason, "Component not found in repository")

		return ctrl.Result{}, fmt.Errorf("failed looking up component: %w", err)
	}

	version, err := r.determineEffectiveVersion(ctx, component, session, repo, c)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.CheckVersionFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	cv, err := session.LookupComponentVersion(repo, c.GetName(), version)
	if err != nil {
		// this version has to exist (since it was found in GetLatestVersion) and therefore, this is most likely a
		// static error where requeueing does not make sense
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get component version: %w", err)
	}

	descriptors, err := r.verifyComponentVersionAndListDescriptors(ctx, octx, component, cv)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.VerificationFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	// Store descriptors and create OCI artifact
	logger.Info("pushing descriptors to storage")
	ociRepositoryName, err := ociartifact.CreateRepositoryName(cv.GetName())
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.CreateOCIRepositoryNameFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	ociRepository, err := r.Registry.NewRepository(ctx, ociRepositoryName)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.CreateOCIRepositoryFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	descriptorsBytes, err := yaml.Marshal(descriptors)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.MarshalFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	tag := ocm.NormalizeVersion(version)
	manifestDigest, err := ociRepository.PushArtifact(ctx, tag, descriptorsBytes)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.PushOCIArtifactFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	// Delete previous artifact version, if any. Note that component's status isn't updated yet.
	err = ociartifact.DeleteIfDigestMismatch(ctx, r.Registry, component, manifestDigest)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, component, v1alpha1.DeleteOCIArtifactFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	ociArtifact := v1alpha1.OCIArtifactInfo{
		Repository: ociRepositoryName,
		Digest:     manifestDigest.String(),
		Blob: &v1alpha1.BlobInfo{
			Digest: digest.FromBytes(descriptorsBytes).String(),
			Tag:    tag,
			Size:   int64(len(descriptorsBytes)),
		},
	}

	logger.Info("updating status")
	component.Status.OCIArtifact = &ociArtifact

	component.Status.Component = v1alpha1.ComponentInfo{
		RepositorySpec: repository.Spec.RepositorySpec,
		Component:      component.Spec.Component,
		Version:        version,
	}

	component.Status.EffectiveOCMConfig = configs

	status.MarkReady(r.EventRecorder, component, "Applied version %s", version)

	return ctrl.Result{RequeueAfter: component.GetRequeueAfter()}, nil
}

func (r *Reconciler) determineEffectiveVersion(ctx context.Context, component *v1alpha1.Component,
	session ocmctx.Session, repo ocmctx.Repository, c ocmctx.ComponentAccess,
) (string, error) {
	versions, err := c.ListVersions()
	if err != nil {
		return "", fmt.Errorf("failed to list versions: %w", err)
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("component %s not found in repository", c.GetName())
	}
	filter, err := ocm.RegexpFilter(component.Spec.SemverFilter)
	if err != nil {
		return "", reconcile.TerminalError(fmt.Errorf("failed to parse regexp filter: %w", err))
	}
	latestSemver, err := ocm.GetLatestValidVersion(ctx, versions, component.Spec.Semver, filter)
	if err != nil {
		return "", reconcile.TerminalError(fmt.Errorf("failed to check latest version: %w", err))
	}

	// we didn't yet reconcile anything, return whatever the retrieved version is.
	if component.Status.Component.Version == "" {
		return latestSemver.Original(), nil
	}

	currentSemver, err := semver.NewVersion(component.Status.Component.Version)
	if err != nil {
		return "", reconcile.TerminalError(fmt.Errorf("failed to check reconciled version: %w", err))
	}

	if latestSemver.GreaterThanEqual(currentSemver) {
		return latestSemver.Original(), nil
	}

	switch component.Spec.DowngradePolicy {
	case v1alpha1.DowngradePolicyDeny:
		return "", reconcile.TerminalError(fmt.Errorf("component version cannot be downgraded from version %s "+
			"to version %s", currentSemver.Original(), latestSemver.Original()))
	case v1alpha1.DowngradePolicyEnforce:
		return latestSemver.Original(), nil
	case v1alpha1.DowngradePolicyAllow:
		reconciledcv, err := session.LookupComponentVersion(repo, c.GetName(), currentSemver.Original())
		if err != nil {
			return "", reconcile.TerminalError(fmt.Errorf("failed to get reconciled component version to check"+
				" downgradability: %w", err))
		}

		latestcv, err := session.LookupComponentVersion(repo, c.GetName(), latestSemver.Original())
		if err != nil {
			return "", fmt.Errorf("failed to get component version: %w", err)
		}

		downgradable, err := ocm.IsDowngradable(ctx, reconciledcv, latestcv)
		if err != nil {
			return "", reconcile.TerminalError(fmt.Errorf("failed to check downgradability: %w", err))
		}
		if !downgradable {
			// keep requeueing, a greater component version could be published
			// semver constraint may even describe older versions and non-existing newer versions, so you have to check
			// for potential newer versions (current is downgradable to: > 1.0.3, latest is: < 1.1.0, but version 1.0.4
			// does not exist yet, but will be created)
			return "", fmt.Errorf("component version cannot be downgraded from version %s "+
				"to version %s", currentSemver.Original(), latestSemver.Original())
		}

		return latestSemver.Original(), nil
	default:
		return "", reconcile.TerminalError(errors.New("unknown downgrade policy: " + string(component.Spec.DowngradePolicy)))
	}
}

func (r *Reconciler) verifyComponentVersionAndListDescriptors(ctx context.Context, octx ocmctx.Context,
	component *v1alpha1.Component, cv ocmctx.ComponentVersionAccess,
) (*ocm.Descriptors, error) {
	logger := log.FromContext(ctx)
	descriptors, err := ocm.VerifyComponentVersion(ctx, cv, sliceutils.Transform(component.Spec.Verify, func(verify v1alpha1.Verification) string {
		return verify.Signature
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to verify component: %w", err)
	}
	logger.Info("component successfully verified", "version", cv.GetVersion(), "component", cv.GetName())

	// if the component descriptors were not collected during signature validation, collect them now
	if descriptors == nil || len(descriptors.List) == 0 {
		descriptors, err = ocm.ListComponentDescriptors(ctx, cv, resolvers.NewCompoundResolver(cv.Repository(), octx.GetResolver()))
		if err != nil {
			return nil, fmt.Errorf("failed to list component descriptors: %w", err)
		}
	}

	return descriptors, nil
}

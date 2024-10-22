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

package replication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"ocm.software/ocm/api/datacontext"
	ocmctx "ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/ocmutils/check"
	"ocm.software/ocm/api/ocm/tools/transfer"
	ocmutils "ocm.software/ocm/api/utils/misc"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/rerror"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

// Reconciler reconciles a Replication object.
type Reconciler struct {
	*ocm.BaseReconciler
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=replications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=replications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=replications/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Replication object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	replication := &v1alpha1.Replication{}
	if err := r.Get(ctx, req.NamespacedName, replication); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper := patch.NewSerialPatcher(replication, r.Client)

	// Always attempt to patch the object and status after each reconciliation.
	defer func() {
		if perr := status.UpdateStatus(ctx, patchHelper, replication, r.EventRecorder, replication.GetRequeueAfter(), retErr); perr != nil {
			retErr = rerror.AsRetryableError(errors.Join(retErr, perr))
		}
	}()

	logger := log.FromContext(ctx)
	if replication.GetDeletionTimestamp() != nil {
		logger.Info("deleting replication", "name", replication.Name)

		return ctrl.Result{}, nil
	}

	if replication.Spec.Suspend {
		logger.Info("replication is suspended, skipping reconciliation")

		return ctrl.Result{}, nil
	}

	if conditions.IsReady(replication) {
		logger.Info("replication already done, skipping reconciliation")

		return ctrl.Result{}, nil
	}

	return rerror.EvaluateReconcileError(r.reconcile(ctx, replication))
}

func (r *Reconciler) reconcile(ctx context.Context, replication *v1alpha1.Replication) (_ ctrl.Result, retErr rerror.ReconcileError) {
	logger := log.FromContext(ctx)

	// get component to be copied
	comp := &v1alpha1.Component{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: replication.Spec.ComponentRef.Namespace,
		Name:      replication.Spec.ComponentRef.Name,
	}, comp); err != nil {
		logger.Info("failed to get component")

		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to get component: %w", err))
	}

	if comp.DeletionTimestamp != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(errors.New("component is being deleted, please do not use it"))
	}

	if !conditions.IsReady(comp) {
		logger.Info("component is not ready", "name", comp.Name)
		status.MarkNotReady(r.EventRecorder, replication, v1alpha1.ComponentIsNotReadyReason, "Component is not ready yet")

		return ctrl.Result{Requeue: true}, nil
	}

	// get target repository
	repo := &v1alpha1.OCMRepository{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: replication.Spec.TargetRepositoryRef.Namespace,
		Name:      replication.Spec.TargetRepositoryRef.Name,
	}, repo); err != nil {
		logger.Info("failed to get repository")

		return ctrl.Result{}, rerror.AsRetryableError(fmt.Errorf("failed to get repository: %w", err))
	}

	if repo.DeletionTimestamp != nil {
		return ctrl.Result{}, rerror.AsNonRetryableError(errors.New("repository is being deleted, please do not use it"))
	}

	if !conditions.IsReady(repo) {
		logger.Info("repository is not ready", "name", repo.Name)
		status.MarkNotReady(r.EventRecorder, replication, v1alpha1.RepositoryIsNotReadyReason, "Repository is not ready yet")

		return ctrl.Result{Requeue: true}, nil
	}

	err := r.transfer(comp, repo)
	if err != nil {
		logger.Info("error transferring component", "component", comp.Name, "targetRepository", repo.Name)
		status.MarkNotReady(r.EventRecorder, replication, v1alpha1.ReplicationFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	status.MarkReady(r.EventRecorder, replication, "Successfully replicated %s:%s to %s", comp.Status.Component.Component, comp.Status.Component.Version, repo.Name)

	return ctrl.Result{}, nil
}

func (r *Reconciler) transfer(comp *v1alpha1.Component, targetOCMRepo *v1alpha1.OCMRepository) (retErr rerror.ReconcileError) {
	// DefaultContext is essentially the same as the extended context created here. The difference is, if we
	// register a new type at an extension point (e.g. a new access type), it's only registered at this exact context
	// instance and not at the global default context variable.
	octx := ocmctx.New(datacontext.MODE_EXTENDED)
	defer func() {
		err := octx.Finalize()
		if err != nil {
			retErr = rerror.AsNonRetryableError(errors.Join(retErr, err))
		}
	}()
	// session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	// octx.Finalizer().Close(session)

	// TODO: configure OCM context

	sourceSpec, err := octx.RepositorySpecForConfig(comp.Status.Component.RepositorySpec.Raw, nil)
	if err != nil {
		return rerror.AsNonRetryableError(fmt.Errorf("cannot create RepositorySpec from raw data: %w", err))
	}

	// sourceRepo, err := session.LookupRepository(octx, sourceSpec)
	sourceRepo, err := octx.RepositoryForSpec(sourceSpec)
	if err != nil {
		return rerror.AsRetryableError(fmt.Errorf("cannot lookup repository for RepositorySpec: %w", err))
	}
	defer sourceRepo.Close()

	cv, err := sourceRepo.LookupComponentVersion(comp.Status.Component.Component, comp.Status.Component.Version)
	if err != nil {
		return rerror.AsRetryableError(fmt.Errorf("cannot lookup component version in source repository: %w", err))
	}
	defer cv.Close()

	targetSpec, err := octx.RepositorySpecForConfig(targetOCMRepo.Spec.RepositorySpec.Raw, nil)
	if err != nil {
		return rerror.AsNonRetryableError(fmt.Errorf("cannot create RepositorySpec from raw data: %w", err))
	}

	// targetRepo, err := session.LookupRepository(octx, targetSpec)
	targetRepo, err := octx.RepositoryForSpec(targetSpec)
	if err != nil {
		return rerror.AsRetryableError(fmt.Errorf("cannot lookup repository for RepositorySpec: %w", err))
	}
	defer targetRepo.Close()

	// TODO: configure TransferOption list

	err = transfer.Transfer(cv, targetRepo)
	if err != nil {
		return rerror.AsRetryableError(fmt.Errorf("cannot transfer component version to target repository: %w", err))
	}

	// check if the component version was transferred successfully
	tcv, err := targetRepo.LookupComponentVersion(comp.Status.Component.Component, comp.Status.Component.Version)
	if err != nil {
		return rerror.AsRetryableError(fmt.Errorf("cannot lookup component version in target repository: %w", err))
	}
	defer tcv.Close()

	// TODO: configure TransferOption list

	result, err := check.Check().ForId(targetRepo, ocmutils.NewNameVersion(comp.Status.Component.Component, comp.Status.Component.Version))
	if err != nil {
		return rerror.AsRetryableError(fmt.Errorf("error checking component version in target repository: %w", err))
	}
	if result != nil {
		msgBytes, err := json.Marshal(result)
		if err != nil {
			return rerror.AsRetryableError(fmt.Errorf("error checking component version in target repository: %s", string(msgBytes)))
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Replication{}).
		Complete(r)
}

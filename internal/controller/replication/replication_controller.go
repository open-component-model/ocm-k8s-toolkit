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

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"k8s.io/apimachinery/pkg/types"
	"ocm.software/ocm/api/datacontext"
	"ocm.software/ocm/api/ocm/ocmutils/check"
	"ocm.software/ocm/api/ocm/tools/transfer"
	"ocm.software/ocm/api/ocm/tools/transfer/transferhandler"
	"ocm.software/ocm/api/ocm/tools/transfer/transferhandler/standard"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ocmctx "ocm.software/ocm/api/ocm"
	ocmutils "ocm.software/ocm/api/utils/misc"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/status"
)

// Reconciler reconciles a Replication object.
type Reconciler struct {
	*ocm.BaseReconciler
}

const (
	componentIndexField  = "spec.componentRef.name"
	targetRepoIndexField = "spec.targetRepositoryRef.name"
)

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
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	replication := &v1alpha1.Replication{}
	if err := r.Get(ctx, req.NamespacedName, replication); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileWithStatusUpdate(ctx, replication)
}

func (r *Reconciler) reconcileWithStatusUpdate(ctx context.Context, replication *v1alpha1.Replication) (ctrl.Result, error) {
	patchHelper := patch.NewSerialPatcher(replication, r.Client)

	result, err := r.reconcileExists(ctx, replication)

	err = errors.Join(err, status.UpdateStatus(ctx, patchHelper, replication, r.EventRecorder, replication.GetRequeueAfter(), err))
	if err != nil {
		return ctrl.Result{}, err
	}

	return result, nil
}

func (r *Reconciler) reconcileExists(ctx context.Context, replication *v1alpha1.Replication) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if replication.GetDeletionTimestamp() != nil {
		logger.Info("replication is being deleted and cannot be used", "name", replication.Name)

		return ctrl.Result{Requeue: true}, nil
	}

	if replication.Spec.Suspend {
		logger.Info("replication is suspended, skipping reconciliation")

		return ctrl.Result{}, nil
	}

	return r.reconcile(ctx, replication)
}

func (r *Reconciler) reconcile(ctx context.Context, replication *v1alpha1.Replication) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// get component to be copied
	comp := &v1alpha1.Component{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: replication.Spec.ComponentRef.Namespace,
		Name:      replication.Spec.ComponentRef.Name,
	}, comp); err != nil {
		logger.Info("failed to get component")

		return ctrl.Result{}, fmt.Errorf("failed to get component: %w", err)
	}

	if !conditions.IsReady(comp) {
		logger.Info("component is not ready", "name", comp.Name)
		status.MarkNotReady(r.EventRecorder, replication, v1alpha1.GetResourceFailedReason, "Component is not ready yet")

		return ctrl.Result{Requeue: true}, nil
	}

	// get target repository
	repo := &v1alpha1.OCMRepository{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: replication.Spec.TargetRepositoryRef.Namespace,
		Name:      replication.Spec.TargetRepositoryRef.Name,
	}, repo); err != nil {
		logger.Info("failed to get repository")

		return ctrl.Result{}, fmt.Errorf("failed to get repository: %w", err)
	}

	if repo.DeletionTimestamp != nil {
		logger.Info("repository is being deleted, please do not use it", "name", repo.Name)

		return ctrl.Result{}, nil
	}

	if !conditions.IsReady(repo) {
		logger.Info("repository is not ready", "name", repo.Name)
		status.MarkNotReady(r.EventRecorder, replication, v1alpha1.GetResourceFailedReason, "Repository is not ready yet")

		return ctrl.Result{Requeue: true}, nil
	}

	if conditions.IsReady(replication) &&
		replication.IsInHistory(comp.Status.Component.Component, comp.Status.Component.Version, string(repo.Spec.RepositorySpec.Raw)) {
		status.MarkReady(r.EventRecorder, replication, "Replicated in previous reconciliations: %s to %s", comp.Name, repo.Name)

		return ctrl.Result{RequeueAfter: replication.GetRequeueAfter()}, nil
	}

	configs, err := ocm.GetEffectiveConfig(ctx, r.GetClient(), replication)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), replication, v1alpha1.ConfigureContextFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	historyRecord, err := r.transfer(ctx, configs, replication, comp, repo)
	if err != nil {
		historyRecord.Error = err.Error()
		historyRecord.EndTime = metav1.Now()
		r.setReplicationStatus(configs, replication, historyRecord)

		logger.Info("error transferring component", "component", comp.Name, "targetRepository", repo.Name)
		status.MarkNotReady(r.EventRecorder, replication, v1alpha1.ReplicationFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	// Update status
	r.setReplicationStatus(configs, replication, historyRecord)
	status.MarkReady(r.EventRecorder, replication, "Successfully replicated %s to %s", comp.Name, repo.Name)

	return ctrl.Result{RequeueAfter: replication.GetRequeueAfter()}, nil
}

func (r *Reconciler) transfer(ctx context.Context, configs []v1alpha1.OCMConfiguration,
	replication *v1alpha1.Replication, comp *v1alpha1.Component, targetOCMRepo *v1alpha1.OCMRepository,
) (historyRecord v1alpha1.TransferStatus, retErr error) {
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

	historyRecord = v1alpha1.TransferStatus{
		StartTime:            metav1.Now(),
		Component:            comp.Status.Component.Component,
		Version:              comp.Status.Component.Version,
		SourceRepositorySpec: string(comp.Status.Component.RepositorySpec.Raw),
		TargetRepositorySpec: string(targetOCMRepo.Spec.RepositorySpec.Raw),
	}

	err := ocm.ConfigureContext(ctx, octx, r.GetClient(), configs)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), replication, v1alpha1.ConfigureContextFailedReason, err.Error())

		return historyRecord, fmt.Errorf("cannot configure context: %w", err)
	}

	sourceRepo, err := session.LookupRepositoryForConfig(octx, comp.Status.Component.RepositorySpec.Raw)
	if err != nil {
		return historyRecord, fmt.Errorf("cannot lookup repository for RepositorySpec: %w", err)
	}

	cv, err := session.LookupComponentVersion(sourceRepo, comp.Status.Component.Component, comp.Status.Component.Version)
	if err != nil {
		return historyRecord, fmt.Errorf("cannot lookup component version in source repository: %w", err)
	}

	targetRepo, err := session.LookupRepositoryForConfig(octx, targetOCMRepo.Spec.RepositorySpec.Raw)
	if err != nil {
		return historyRecord, fmt.Errorf("cannot lookup repository for RepositorySpec: %w", err)
	}

	// Extract transfer options from OCM Context
	opts := &standard.Options{}
	err = transferhandler.From(octx, opts)
	if err != nil {
		return historyRecord, fmt.Errorf("cannot retrieve transfer options from OCM context: %w", err)
	}

	err = transfer.Transfer(cv, targetRepo, opts)
	if err != nil {
		return historyRecord, fmt.Errorf("cannot transfer component version to target repository: %w", err)
	}

	// the transfer operation can only be considered successful, if the copied component can be successfully verified in the target repository
	err = r.validate(session, targetRepo, comp.Status.Component.Component, comp.Status.Component.Version)
	if err != nil {
		return historyRecord, err
	}

	historyRecord.Success = true
	historyRecord.EndTime = metav1.Now()

	return historyRecord, nil
}

// validate checks if the component version can be found in the repository
// and if it is completely (with dependent component references) contained in the target OCM repository.
// If this is not the case an error is returned.
// In the future this function should also verify the component's signature.
func (r *Reconciler) validate(session ocmctx.Session, repo ocmctx.Repository, compName string, compVersion string) error {
	// check if component version can be found in the repository
	_, err := session.LookupComponentVersion(repo, compName, compVersion)
	if err != nil {
		return fmt.Errorf("cannot lookup component version in repository: %w", err)
	}

	// 'check.Check()' provides the same functionality as the 'ocm check cv' CLI command.
	// See also: https://github.com/open-component-model/ocm/blob/main/docs/reference/ocm_check_componentversions.md
	// TODO: configure '--local-resources' and '--local-sources', if respective transfer options are set
	// (see https://github.com/open-component-model/ocm-project/issues/343)
	result, err := check.Check().ForId(repo, ocmutils.NewNameVersion(compName, compVersion))
	if err != nil {
		return fmt.Errorf("cannot verify that component version exists in repository: %w", err)
	}
	if !result.IsEmpty() {
		msgBytes, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("cannot marshal the result of component version check in repository: %w", err)
		}

		return fmt.Errorf("component version is not completely contained in repository: %s", string(msgBytes))
	}

	// TODO: verify component's signature in target repository (if component is signed)
	// (see https://github.com/open-component-model/ocm-project/issues/344)

	return nil
}

func (r *Reconciler) setReplicationStatus(configs []v1alpha1.OCMConfiguration, replication *v1alpha1.Replication, historyRecord v1alpha1.TransferStatus) {
	replication.AddHistoryRecord(historyRecord)

	replication.Status.EffectiveOCMConfig = configs
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Create index for component name
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.Replication{}, componentIndexField, componentNameExtractor); err != nil {
		return err
	}

	// Create index for target repository name
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.Replication{}, targetRepoIndexField, targetRepoNameExtractor); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Replication{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&v1alpha1.Component{}, handler.EnqueueRequestsFromMapFunc(r.replicationMapFunc)).
		Watches(&v1alpha1.OCMRepository{}, handler.EnqueueRequestsFromMapFunc(r.replicationMapFunc)).
		Complete(r)
}

func componentNameExtractor(obj client.Object) []string {
	replication, ok := obj.(*v1alpha1.Replication)
	if !ok {
		return nil
	}

	return []string{replication.Spec.ComponentRef.Name}
}

func targetRepoNameExtractor(obj client.Object) []string {
	replication, ok := obj.(*v1alpha1.Replication)
	if !ok {
		return nil
	}

	return []string{replication.Spec.TargetRepositoryRef.Name}
}

func componentMatchingFields(component *v1alpha1.Component) client.MatchingFields {
	return client.MatchingFields{componentIndexField: component.GetName()}
}

func targetRepoMatchingFields(repo *v1alpha1.OCMRepository) client.MatchingFields {
	return client.MatchingFields{targetRepoIndexField: repo.GetName()}
}

func (r *Reconciler) replicationMapFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	var fields client.MatchingFields
	replList := &v1alpha1.ReplicationList{}

	component, ok := obj.(*v1alpha1.Component)
	if ok {
		fields = componentMatchingFields(component)
	} else if repo, ok := obj.(*v1alpha1.OCMRepository); ok {
		fields = targetRepoMatchingFields(repo)
	} else {
		return []reconcile.Request{}
	}

	if err := r.List(ctx, replList, fields); err != nil {
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, 0, len(replList.Items))
	for _, replication := range replList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: replication.GetNamespace(),
				Name:      replication.GetName(),
			},
		})
	}

	return requests
}

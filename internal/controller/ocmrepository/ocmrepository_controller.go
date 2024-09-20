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

package ocmrepository

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxcd/pkg/runtime/patch"
	"ocm.software/ocm/api/datacontext"
	ocmctx "ocm.software/ocm/api/ocm"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	v1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/rerror"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

// OCMRepositoryReconciler reconciles a OCMRepository object.
type OCMRepositoryReconciler struct {
	*ocm.BaseReconciler
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmrepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmrepositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmrepositories/finalizers,verbs=update

func (r *OCMRepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	ocmRepo := &v1alpha1.OCMRepository{}
	if err := r.Get(ctx, req.NamespacedName, ocmRepo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger := log.FromContext(ctx)
	if ocmRepo.GetDeletionTimestamp() != nil {
		logger.Info("deleting component", "name", ocmRepo.Name)

		return ctrl.Result{}, nil
	}

	if ocmRepo.Spec.Suspend {
		logger.Info("component is suspended, skipping reconciliation")

		return ctrl.Result{}, nil
	}

	return rerror.EvaluateReconcileError(r.reconsile(ctx, ocmRepo))
}

func (r *OCMRepositoryReconciler) reconsile(ctx context.Context, ocmRepo *v1alpha1.OCMRepository) (_ ctrl.Result, retErr rerror.ReconcileError) {
	patchHelper := patch.NewSerialPatcher(ocmRepo, r.Client)

	// Always attempt to patch the object and status after each reconciliation.
	defer func() {
		if perr := status.UpdateStatus(ctx, patchHelper, ocmRepo, r.EventRecorder, ocmRepo.GetRequeueAfter(), retErr); perr != nil {
			retErr = rerror.AsRetryableError(errors.Join(retErr, perr))
		}
	}()

	var err error
	var rerr rerror.ReconcileError
	// DefaultContext is essentially the same as the extended context created here. The difference is, if we
	// register a new type at an extension point (e.g. a new access type), it's only registered at this exact context
	// instance and not at the global default context variable.
	octx := ocmctx.New(datacontext.MODE_EXTENDED)
	defer func() {
		err = octx.Finalize()
		if err != nil {
			retErr = rerror.AsNonRetryableError(errors.Join(retErr, err))
		}
	}()
	session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

	rerr = ocm.ConfigureOCMContext(ctx, r, octx, ocmRepo, ocmRepo)
	if rerr != nil {
		return ctrl.Result{}, rerr
	}

	rerr = r.validate(octx, session, ocmRepo)
	if rerr != nil {
		return ctrl.Result{}, rerr
	}

	ocmRepo.Status.RepositorySpec = ocmRepo.Spec.RepositorySpec

	status.MarkReady(r.EventRecorder, ocmRepo, "Successfully reconciled")
	return ctrl.Result{}, nil
}

func (r *OCMRepositoryReconciler) validate(octx ocmctx.Context, session ocmctx.Session, ocmRepo *v1alpha1.OCMRepository) (retErr rerror.ReconcileError) {
	spec, err := octx.RepositorySpecForConfig(ocmRepo.Spec.RepositorySpec.Raw, nil)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, ocmRepo, v1alpha1.RepositorySpecInvalidReason, "cannot create RepositorySpec from raw data")
		return rerror.AsNonRetryableError(fmt.Errorf("cannot create RepositorySpec from raw data: %w", err))
	}

	if err = spec.Validate(octx, nil); err != nil {
		status.MarkNotReady(r.EventRecorder, ocmRepo, v1alpha1.RepositorySpecInvalidReason, "invalid RepositorySpec")
		return rerror.AsNonRetryableError(fmt.Errorf("invalid RepositorySpec: %w", err))
	}

	_, err = session.LookupRepositoryForConfig(octx, ocmRepo.Spec.RepositorySpec.Raw)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, ocmRepo, v1alpha1.RepositorySpecInvalidReason, "cannot lookup repository for RepositorySpec")
		return rerror.AsRetryableError(fmt.Errorf("cannot lookup repository for RepositorySpec: %w", err))
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *OCMRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.OCMRepository{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

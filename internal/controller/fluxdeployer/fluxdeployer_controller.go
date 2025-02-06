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

package fluxdeployer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/events"
	"github.com/fluxcd/pkg/runtime/patch"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
	eventv1 "github.com/fluxcd/pkg/apis/event/v1beta1"
	sourcev1beta2 "github.com/fluxcd/source-controller/api/v1beta2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/event"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

const (
	refreshInterval = 10 * time.Second
)

// Reconciler reconciles a FluxDeployer object.
type Reconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	EventRecorder  *events.Recorder
	Registry       string
	CertSecretName string // TODO: Set this to the cert for the registry.
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=fluxdeployers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=fluxdeployers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=fluxdeployers/finalizers,verbs=update

// Reconcile loop for FluxDeployer.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (_ ctrl.Result, err error) {
	obj := &v1alpha1.FluxDeployer{}
	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get deployer object: %w", err)
	}

	patchHelper := patch.NewSerialPatcher(obj, r.Client)

	// Always attempt to patch the object and status after each reconciliation.
	defer func() {
		if derr := status.UpdateStatus(ctx, patchHelper, obj, r.EventRecorder, obj.GetRequeueAfter(), err); derr != nil {
			err = errors.Join(err, derr)
		}
	}()

	return r.reconcile(ctx, obj)
}

func (r *Reconciler) reconcile(
	ctx context.Context,
	obj *v1alpha1.FluxDeployer,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("reconciling flux-deployer", "name", obj.GetName())

	// get snapshot
	snapshot, err := r.getSnapshot(ctx, obj)
	if err != nil {
		logger.Info("could not find source ref", "name", obj.Spec.SourceRef.Name, "err", err)

		// Try again in 10 seconds instead of the configured interval to avoid too long waiting times.
		// Don't use exponential backoff because it batters the api for no good reason.
		return ctrl.Result{RequeueAfter: refreshInterval}, nil
	}

	// requeue if snapshot is not ready
	if conditions.IsFalse(snapshot, meta.ReadyCondition) {
		logger.Info("snapshot not ready yet", "snapshot", snapshot.Name)

		return ctrl.Result{RequeueAfter: obj.GetRequeueAfter()}, nil
	}

	// TODO: Make the OCI registry URL part of the snapshot.
	snapshotURL := fmt.Sprintf("oci://%s/%s", r.Registry, snapshot.Spec.Repository)
	if obj.Spec.KustomizationTemplate != nil && obj.Spec.HelmReleaseTemplate != nil {
		return ctrl.Result{}, fmt.Errorf(
			"can't define both kustomization template and helm release template",
		)
	}

	// create kustomization
	if obj.Spec.KustomizationTemplate != nil {
		// can't check for helm content as we don't know where things are or what content to check for
		if err := r.createKustomizationSources(ctx, obj, snapshot.Spec.Repository, snapshot.Spec.Blob.Tag); err != nil {
			msg := "failed to create kustomization sources"
			logger.Error(err, msg)
			conditions.MarkFalse(
				obj,
				meta.ReadyCondition,
				"CreateOrUpdateKustomizationFailed",
				"create kustomize failed with %s", err.Error(),
			)
			conditions.MarkStalled(
				obj,
				"CreateOrUpdateKustomizationFailed",
				"create kustomize failed with %s", err.Error(),
			)
			event.New(r.EventRecorder, obj, nil, eventv1.EventSeverityError, msg)

			return ctrl.Result{}, err
		}
	}

	if obj.Spec.HelmReleaseTemplate != nil {
		tag := snapshot.Spec.Blob.Tag
		if err := r.createHelmSources(ctx, obj, snapshotURL, tag); err != nil {
			msg := "failed to create helm sources"
			logger.Error(err, msg)
			conditions.MarkFalse(
				obj,
				meta.ReadyCondition,
				"CreateOrUpdateHelmFailed",
				"create or update helm failed with %s", err.Error(),
			)
			conditions.MarkStalled(obj, "CreateOrUpdateHelmFailed", "create or update helm failed with %s", err.Error())
			event.New(r.EventRecorder, obj, nil, eventv1.EventSeverityError, msg)

			return ctrl.Result{}, err
		}
	}

	// if wait for ready, make sure all created objects are ready and existing.
	if obj.Spec.WaitForReady {
		var objs []conditions.Getter

		if err := r.findHelmRelease(ctx, obj, &objs); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to find helm release: %w", err)
		}

		if err := r.findOCIRepository(ctx, obj, &objs); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to find oci repository: %w", err)
		}

		if err := r.findKustomization(ctx, obj, &objs); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to find kustomization: %w", err)
		}

		for _, o := range objs {
			if !conditions.IsReady(o) {
				conditions.MarkFalse(
					obj,
					meta.ReadyCondition,
					"CreatedObjectsNotReady",
					"waiting for ready condition on created sources",
				)

				return ctrl.Result{RequeueAfter: obj.Spec.Interval.Duration}, nil
			}
		}
	}

	status.MarkReady(r.EventRecorder, obj, "FluxDeployer '%s' is ready", obj.Name)

	return ctrl.Result{}, nil
}

func (r *Reconciler) createKustomizationSources(
	ctx context.Context,
	obj *v1alpha1.FluxDeployer,
	url, tag string,
) error {
	// create FluxCDs OCIRepository
	if err := r.reconcileOCIRepo(ctx, obj, url, tag); err != nil {
		return fmt.Errorf("failed to create OCI repository: %w", err)
	}

	if err := r.reconcileKustomization(ctx, obj); err != nil {
		return fmt.Errorf("failed to create Kustomization object :%w", err)
	}

	return nil
}

func (r *Reconciler) createHelmSources(
	ctx context.Context,
	obj *v1alpha1.FluxDeployer,
	url, tag string,
) error {
	// create FluxCDs OCIRepository
	if err := r.reconcileOCIRepo(ctx, obj, url, tag); err != nil {
		return fmt.Errorf("failed to create OCI repository: %w", err)
	}

	if err := r.reconcileHelmRelease(ctx, obj); err != nil {
		return fmt.Errorf("failed to create Helm Release object :%w", err)
	}

	return nil
}

func (r *Reconciler) reconcileOCIRepo(
	ctx context.Context,
	obj *v1alpha1.FluxDeployer,
	url, tag string,
) error {
	ociRepoCR := &sourcev1beta2.OCIRepository{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ociRepoCR, func() error {
		if ociRepoCR.ObjectMeta.CreationTimestamp.IsZero() {
			if err := controllerutil.SetOwnerReference(obj, ociRepoCR, r.Scheme); err != nil {
				return fmt.Errorf("failed to set owner reference on oci repository source: %w", err)
			}
		}
		ociRepoCR.Spec = sourcev1beta2.OCIRepositorySpec{
			Interval: obj.Spec.Interval,
			CertSecretRef: &meta.LocalObjectReference{
				Name: r.CertSecretName,
			},
			URL: url,
			Reference: &sourcev1beta2.OCIRepositoryRef{
				Tag: tag,
			},
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create reconcile oci repo: %w", err)
	}

	obj.Status.OCIRepository = ociRepoCR.GetNamespace() + "/" + ociRepoCR.GetName()

	return nil
}

func (r *Reconciler) reconcileKustomization(
	ctx context.Context,
	obj *v1alpha1.FluxDeployer,
) error {
	kust := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, kust, func() error {
		if kust.ObjectMeta.CreationTimestamp.IsZero() {
			if err := controllerutil.SetOwnerReference(obj, kust, r.Scheme); err != nil {
				return fmt.Errorf("failed to set owner reference on oci repository source: %w", err)
			}
		}
		kust.Spec = *obj.Spec.KustomizationTemplate
		kust.Spec.SourceRef.Kind = sourcev1beta2.OCIRepositoryKind
		kust.Spec.SourceRef.Namespace = obj.GetNamespace()
		kust.Spec.SourceRef.Name = obj.GetName()

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create reconcile kustomization: %w", err)
	}

	obj.Status.Kustomization = kust.GetNamespace() + "/" + kust.GetName()

	return nil
}

func (r *Reconciler) getSnapshot(
	ctx context.Context,
	obj *v1alpha1.FluxDeployer,
) (*v1alpha1.Snapshot, error) {
	var src client.Object = &unstructured.Unstructured{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: obj.Spec.SourceRef.Name, Namespace: obj.Spec.SourceRef.Namespace}, src); err != nil {
		return nil, fmt.Errorf("failed to get snapshot: %w", err)
	}

	writer, ok := src.(v1alpha1.SnapshotWriter)
	if !ok {
		return nil, fmt.Errorf("invalid snapshot type: %T", src)
	}

	return snapshot.GetSnapshotForOwner(ctx, r.Client, writer)
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	const (
		sourceKey = ".metadata.source"
	)

	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.FluxDeployer{}, sourceKey, func(rawObj client.Object) []string {
		obj, ok := rawObj.(*v1alpha1.FluxDeployer)
		if !ok {
			return []string{}
		}
		ns := obj.Spec.SourceRef.Namespace
		if ns == "" {
			ns = obj.GetNamespace()
		}

		return []string{fmt.Sprintf("%s/%s", ns, obj.Spec.SourceRef.Name)}
	}); err != nil {
		return fmt.Errorf("failed setting index fields: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.FluxDeployer{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&v1alpha1.Snapshot{},
			handler.EnqueueRequestsFromMapFunc(r.findObjects(sourceKey)),
			builder.WithPredicates(SnapshotDigestChangedPredicate{}),
		).
		Complete(r)
}

func (r *Reconciler) findObjects(sourceKey string) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		var selectorTerm string
		switch obj.(type) {
		case *v1alpha1.Snapshot:
			if len(obj.GetOwnerReferences()) != 1 {
				return []reconcile.Request{}
			}
			selectorTerm = fmt.Sprintf("%s/%s", obj.GetNamespace(), obj.GetOwnerReferences()[0].Name)
		default:
			return []reconcile.Request{}
		}

		sourceRefs := &v1alpha1.FluxDeployerList{}
		if err := r.List(ctx, sourceRefs, &client.ListOptions{
			FieldSelector: fields.OneTermEqualSelector(sourceKey, selectorTerm),
		}); err != nil {
			return []reconcile.Request{}
		}

		requests := make([]reconcile.Request, len(sourceRefs.Items))
		for i, item := range sourceRefs.Items {
			requests[i] = reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: item.GetNamespace(),
					Name:      item.GetName(),
				},
			}
		}

		return requests
	}
}

func (r *Reconciler) reconcileHelmRelease(
	ctx context.Context,
	obj *v1alpha1.FluxDeployer,
) error {
	helmRelease := &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, helmRelease, func() error {
		if helmRelease.ObjectMeta.CreationTimestamp.IsZero() {
			if err := controllerutil.SetControllerReference(obj, helmRelease, r.Scheme); err != nil {
				return fmt.Errorf("failed to set controller reference on oci repository source: %w", err)
			}
		}
		helmRelease.Spec = *obj.Spec.HelmReleaseTemplate
		helmRelease.Spec.ChartRef = &helmv2.CrossNamespaceSourceReference{
			Kind:      "OCIRepository",
			Name:      obj.GetName(),
			Namespace: obj.GetNamespace(),
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create reconcile kustomization: %w", err)
	}

	obj.Status.HelmRelease = helmRelease.GetNamespace() + "/" + helmRelease.GetName()

	return nil
}

func (r *Reconciler) findHelmRelease(ctx context.Context, obj *v1alpha1.FluxDeployer, objs *[]conditions.Getter) error {
	if obj.Status.HelmRelease == "" {
		return nil
	}

	helmRelease := &helmv2.HelmRelease{}
	split := strings.Split(obj.Status.HelmRelease, "/")
	if len(split) == 0 || len(split) != 2 {
		return fmt.Errorf("failed to find helm release in status: %s", obj.Status.HelmRelease)
	}

	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: split[0], Name: split[1]}, helmRelease); err != nil {
		return fmt.Errorf("failed to find helm release: %w", err)
	}

	*objs = append(*objs, helmRelease)

	return nil
}

func (r *Reconciler) findOCIRepository(ctx context.Context, obj *v1alpha1.FluxDeployer, objs *[]conditions.Getter) error {
	if obj.Status.OCIRepository == "" {
		return nil
	}

	ociRepo := &sourcev1beta2.OCIRepository{}
	split := strings.Split(obj.Status.OCIRepository, "/")
	if len(split) == 0 || len(split) != 2 {
		return fmt.Errorf("failed to find oci repository in status: %s", obj.Status.OCIRepository)
	}

	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: split[0], Name: split[1]}, ociRepo); err != nil {
		return fmt.Errorf("failed to find oci repository: %w", err)
	}

	*objs = append(*objs, ociRepo)

	return nil
}

func (r *Reconciler) findKustomization(ctx context.Context, obj *v1alpha1.FluxDeployer, objs *[]conditions.Getter) error {
	if obj.Status.Kustomization == "" {
		return nil
	}

	kustomization := &kustomizev1.Kustomization{}
	split := strings.Split(obj.Status.Kustomization, "/")
	if len(split) == 0 || len(split) != 2 {
		return fmt.Errorf("failed to find kustomization in status: %s", obj.Status.Kustomization)
	}

	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: split[0], Name: split[1]}, kustomization); err != nil {
		return fmt.Errorf("failed to find kustomization: %w", err)
	}

	*objs = append(*objs, kustomization)

	return nil
}

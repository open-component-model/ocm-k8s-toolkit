package helpers

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
)

type OCMK8SReconciler interface {
	GetClient() ctrl.Client
	GetScheme() *runtime.Scheme
	GetEventRecorder() record.EventRecorder
}

type OCMK8SBaseReconciler struct {
	ctrl.Client
	Scheme *runtime.Scheme
	record.EventRecorder
}

func (r *OCMK8SBaseReconciler) GetClient() ctrl.Client {
	return r.Client
}

func (r *OCMK8SBaseReconciler) GetScheme() *runtime.Scheme {
	return r.Scheme
}

func (r *OCMK8SBaseReconciler) GetEventRecorder() record.EventRecorder {
	return r.EventRecorder
}

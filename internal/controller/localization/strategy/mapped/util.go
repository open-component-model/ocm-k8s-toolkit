package mapped

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

type ObjectPointerType[T any] interface {
	*T
	ctrl.Object
}

func getNamespaced[T any, P ObjectPointerType[T]](ctx context.Context, client ctrl.Reader, key v1.LocalObjectReference, namespace string) (P, error) {
	var _obj T
	obj := P(&_obj)
	if err := client.Get(ctx, ctrl.ObjectKey{Namespace: namespace, Name: key.Name}, obj); err != nil {
		return nil, fmt.Errorf("failed to locate object: %w", err)
	}

	return obj, nil
}

func get[T any, P ObjectPointerType[T]](ctx context.Context, client ctrl.Reader, key v1alpha1.ObjectKey) (P, error) {
	var _obj T
	obj := P(&_obj)
	if err := client.Get(ctx, ctrl.ObjectKey(key), obj); err != nil {
		return nil, fmt.Errorf("failed to locate object: %w", err)
	}

	return obj, nil
}

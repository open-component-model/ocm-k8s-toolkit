package util

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/template"

	"github.com/containers/image/v5/pkg/compression"
	"github.com/fluxcd/pkg/runtime/conditions"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
)

type NotReadyError struct {
	objectName string
}

func (e NotReadyError) Error() string {
	return fmt.Sprintf("object is not ready: %s", e.objectName)
}

type DeletionError struct {
	objectName string
}

func (e DeletionError) Error() string {
	return fmt.Sprintf("object is being deleted: %s", e.objectName)
}

type Getter interface {
	GetConditions() []metav1.Condition
	ctrl.Object
}

type ObjectPointerType[T any] interface {
	*T
	Getter
}

func GetNamespaced[T any, P ObjectPointerType[T]](ctx context.Context, client ctrl.Reader, key v1.LocalObjectReference, namespace string) (P, error) {
	obj := P(new(T))
	if err := client.Get(ctx, ctrl.ObjectKey{Namespace: namespace, Name: key.Name}, obj); err != nil {
		return nil, fmt.Errorf("failed to locate object: %w", err)
	}

	return obj, nil
}

func Get[T any, P ObjectPointerType[T]](ctx context.Context, client ctrl.Reader, key ctrl.ObjectKey) (P, error) {
	obj := P(new(T))
	if err := client.Get(ctx, key, obj); err != nil {
		return nil, fmt.Errorf("failed to locate object: %w", err)
	}

	return obj, nil
}

func GetReadyObject[T any, P ObjectPointerType[T]](ctx context.Context, client ctrl.Reader, key ctrl.ObjectKey) (P, error) {
	obj := P(new(T))
	if err := client.Get(ctx, key, obj); err != nil {
		return nil, fmt.Errorf("failed to locate object: %w", err)
	}

	if !obj.GetDeletionTimestamp().IsZero() {
		return nil, DeletionError{key.String()}
	}

	if !conditions.IsReady(obj) {
		return nil, NotReadyError{key.String()}
	}

	return obj, nil
}

// Parse reads the config from the source and decodes it using a decoder
// It autodecompresses the source and reads the config via DataFromTarOrPlain.
// This allows the source to be either
// - a plain yaml file in a (compressed or uncompressed) tar archive
// - a plain yaml file
//
// Note that if the tar archive contains multiple files, only the first one is read as stored in the tar.
func Parse[T any, P ObjectPointerType[T]](source io.Reader, decoder runtime.Decoder) (_ P, err error) {
	decompressed, _, err := compression.AutoDecompress(source)
	if err != nil {
		return nil, fmt.Errorf("failed to autodecompress config: %w", err)
	}
	defer func() {
		err = errors.Join(err, decompressed.Close())
	}()
	data, err := DataFromTarOrPlain(decompressed)
	if err != nil {
		return nil, fmt.Errorf("failed to get data from tar or plain: %w", err)
	}

	cfg, err := io.ReadAll(data)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	obj, _, err := decoder.Decode(cfg, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}
	cfgObj, ok := obj.(P)
	if !ok {
		return nil, fmt.Errorf("failed to decode config (not a valid LocalizationConfig): %w", err)
	}

	return cfgObj, nil
}

// KubernetesObjectReferenceTemplateFunc creates a template function map that can be used in a GoTemplate
// to resolve a Kubernetes object reference from the cluster.
// Example:
// {{ KubernetesObjectReference "my-namespace" "my-name" }}
// this looks up the object with the name "my-name" in the namespace "my-namespace" and returns the object as JSON.
// Note that this usually requires coupling with the sprig "mustFromJson" function to parse the object.
func KubernetesObjectReferenceTemplateFunc(
	ctx context.Context,
	clnt ctrl.Reader,
) template.FuncMap {
	return template.FuncMap{
		"KubernetesObjectReference": func(namespace, name string) (any, error) {
			obj := &unstructured.Unstructured{}
			if err := clnt.Get(ctx, ctrl.ObjectKey{Name: name, Namespace: namespace}, obj); err != nil {
				return "", fmt.Errorf("failed to Get object: %w", err)
			}

			return obj.UnstructuredContent(), nil
		},
	}
}

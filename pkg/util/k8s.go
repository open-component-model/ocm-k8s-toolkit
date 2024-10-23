package util

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/containers/image/v5/pkg/compression"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

type ObjectPointerType[T any] interface {
	*T
	ctrl.Object
}

func GetNamespaced[T any, P ObjectPointerType[T]](ctx context.Context, client ctrl.Reader, key v1.LocalObjectReference, namespace string) (P, error) {
	obj := P(new(T))
	if err := client.Get(ctx, ctrl.ObjectKey{Namespace: namespace, Name: key.Name}, obj); err != nil {
		return nil, fmt.Errorf("failed to locate object: %w", err)
	}

	return obj, nil
}

func Get[T any, P ObjectPointerType[T]](ctx context.Context, client ctrl.Reader, key v1alpha1.ObjectKey) (P, error) {
	obj := P(new(T))
	if err := client.Get(ctx, ctrl.ObjectKey(key), obj); err != nil {
		return nil, fmt.Errorf("failed to locate object: %w", err)
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

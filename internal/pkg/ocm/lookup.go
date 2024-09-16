package ocm

import (
	"context"
	"fmt"

	"github.com/mandelsoft/goutils/sliceutils"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// GetEffectiveSecretRefs returns either the secrets from obj's spec or the effective secrets of the def (= default)
// SecretRefProvider.
func GetEffectiveSecretRefs(_ context.Context,
	obj v1alpha1.SecretRefProvider, def ...v1alpha1.SecretRefProvider,
) []ctrl.ObjectKey {
	ns := obj.GetNamespace()
	refs := obj.GetSecretRefs()

	// if no secrets were specified on the object itself, default to the effective secrets of the default object
	// (which is typically the predecessor object in the processing chain, so for a component, it's a repository)
	if len(refs) == 0 && len(def) > 0 {
		ns = def[0].GetNamespace()
		refs = def[0].GetEffectiveSecretRefs()
	}

	secretRefs := sliceutils.Transform(refs, func(ref corev1.LocalObjectReference) ctrl.ObjectKey {
		return ctrl.ObjectKey{
			Namespace: ns,
			Name:      ref.Name,
		}
	})

	return secretRefs
}

// GetEffectiveConfigRefs returns either the configs from obj's spec or the effective configs of the def (= default)
// ConfigRefProvider.
func GetEffectiveConfigRefs(_ context.Context,
	obj v1alpha1.ConfigRefProvider, def ...v1alpha1.ConfigRefProvider,
) []ctrl.ObjectKey {
	ns := obj.GetNamespace()
	refs := obj.GetConfigRefs()

	// if no secrets were specified on the object itself, default to the effective secrets of the default object
	// (which is typically the predecessor object in the processing chain, so for a component, it's a repository)
	if len(refs) == 0 && len(def) > 0 {
		ns = def[0].GetNamespace()
		refs = def[0].GetEffectiveConfigRefs()
	}

	configRefs := sliceutils.Transform(refs, func(ref corev1.LocalObjectReference) ctrl.ObjectKey {
		return ctrl.ObjectKey{
			Namespace: ns,
			Name:      ref.Name,
		}
	})

	return configRefs
}

func GetEffectiveConfigSet(_ context.Context,
	obj v1alpha1.ConfigSetProvider, def v1alpha1.ConfigSetProvider,
) string {
	set := obj.GetConfigSet()
	if set != nil {
		return *set
	}

	return def.GetEffectiveConfigSet()
}

// GetSecrets returns the secrets referenced by the secretRefs.
func GetSecrets(ctx context.Context, client ctrl.Client,
	secretRefs []ctrl.ObjectKey,
) ([]*corev1.Secret, error) {
	secrets, err := get[corev1.Secret](ctx, client, secretRefs)
	if err != nil {
		return nil, err
	}

	return secrets, nil
}

// GetConfigMaps returns the secrets referenced by the secretRefs.
func GetConfigMaps(ctx context.Context, client ctrl.Client,
	configRefs []ctrl.ObjectKey,
) ([]*corev1.ConfigMap, error) {
	configs, err := get[corev1.ConfigMap](ctx, client, configRefs)
	if err != nil {
		return nil, err
	}

	return configs, nil
}

type ObjectPointerType[T any] interface {
	*T
	ctrl.Object
}

func get[T any, P ObjectPointerType[T]](ctx context.Context, client ctrl.Client,
	refs []ctrl.ObjectKey,
) ([]P, error) {
	objs := make([]P, len(refs))
	i := 0
	for _, ref := range refs {
		var _obj T
		obj := P(&_obj)

		if err := client.Get(ctx, ref, obj); err != nil {
			return nil, fmt.Errorf("failed to locate object: %w", err)
		}
		objs[i] = obj
	}

	return objs, nil
}

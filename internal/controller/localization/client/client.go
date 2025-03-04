package client

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/types"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
)

type Client interface {
	client.Reader

	// Scheme returns the scheme this client is using.
	Scheme() *runtime.Scheme

	GetLocalizationTarget(ctx context.Context, ref v1alpha1.ConfigurationReference) (target types.LocalizationTarget, err error)
	// GetLocalizationConfig fetches the localization config from Kubernetes.
	// Compared to the LocalizationTarget, the Config Strategy can be used to further determine how to treat the source of the localization.
	// Based on the APIVersion & Kind of the reference, it will fetch the source from the Kubernetes.
	GetLocalizationConfig(ctx context.Context, ref v1alpha1.ConfigurationReference) (source types.LocalizationConfig, err error)
}

func NewClientWithRegistry(c client.Client, registry *ociartifact.Registry, scheme *runtime.Scheme) Client {
	factory := serializer.NewCodecFactory(scheme)
	info, _ := runtime.SerializerInfoForMediaType(factory.SupportedMediaTypes(), runtime.ContentTypeYAML)
	encoder := factory.EncoderForVersion(info.Serializer, v1alpha1.GroupVersion)

	return &localStorageBackedClient{
		Client:   c,
		Registry: registry,
		scheme:   scheme,
		encoder:  encoder,
	}
}

type localStorageBackedClient struct {
	client.Client
	Registry *ociartifact.Registry
	scheme   *runtime.Scheme
	encoder  runtime.Encoder
}

func (clnt *localStorageBackedClient) Scheme() *runtime.Scheme {
	return clnt.scheme
}

var _ Client = &localStorageBackedClient{}

func (clnt *localStorageBackedClient) GetLocalizationTarget(
	ctx context.Context,
	ref v1alpha1.ConfigurationReference,
) (types.LocalizationTarget, error) {
	switch ref.Kind {
	case v1alpha1.KindConfiguredResource:
		fallthrough
	case v1alpha1.KindLocalizedResource:
		fallthrough
	case v1alpha1.KindResource:
		return ociartifact.GetContentBackedByArtifactFromComponent(ctx, clnt.Client, clnt.Registry, &ref)
	default:
		return nil, fmt.Errorf("unsupported localization target kind: %s", ref.Kind)
	}
}

func (clnt *localStorageBackedClient) GetLocalizationConfig(
	ctx context.Context,
	ref v1alpha1.ConfigurationReference,
) (types.LocalizationConfig, error) {
	switch ref.Kind {
	case v1alpha1.KindResource:
		return ociartifact.GetContentBackedByArtifactFromComponent(ctx, clnt.Client, clnt.Registry, &ref)
	case v1alpha1.KindLocalizationConfig:
		return GetLocalizationConfigFromKubernetes(ctx, clnt.Client, clnt.encoder, ref)
	default:
		return nil, fmt.Errorf("unsupported localization config kind: %s", ref.Kind)
	}
}

func GetLocalizationConfigFromKubernetes(ctx context.Context, clnt client.Reader, encoder runtime.Encoder, reference v1alpha1.ConfigurationReference) (types.LocalizationConfig, error) {
	if reference.APIVersion == "" {
		reference.APIVersion = v1alpha1.GroupVersion.String()
	}
	if reference.APIVersion != v1alpha1.GroupVersion.String() || reference.Kind != "LocalizationConfig" {
		return nil, fmt.Errorf("unsupported localization target reference: %s/%s", reference.APIVersion, reference.Kind)
	}

	cfg := v1alpha1.LocalizationConfig{}
	if err := clnt.Get(ctx, client.ObjectKey{
		Namespace: reference.Namespace,
		Name:      reference.Name,
	}, &cfg); err != nil {
		return nil, fmt.Errorf("failed to fetch localization config %s: %w", reference.Name, err)
	}

	return &ociartifact.ObjectConfig{Object: &cfg, Encoder: encoder}, nil
}

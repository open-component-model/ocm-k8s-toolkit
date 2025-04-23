package client

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/configuration/types"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
)

type Client interface {
	client.Reader

	// Scheme returns the scheme this client is using.
	Scheme() *runtime.Scheme

	GetConfiguration(ctx context.Context, ref v1alpha1.ConfigurationReference) (source types.ConfigurationSource, err error)
	GetTarget(ctx context.Context, ref v1alpha1.ConfigurationReference) (target types.ConfigurationTarget, err error)
}

func NewClientWithRegistry(r client.Reader, registry *ociartifact.Registry, scheme *runtime.Scheme) Client {
	factory := serializer.NewCodecFactory(scheme)
	info, _ := runtime.SerializerInfoForMediaType(factory.SupportedMediaTypes(), runtime.ContentTypeYAML)
	encoder := factory.EncoderForVersion(info.Serializer, v1alpha1.GroupVersion)

	return &localStorageBackedClient{
		Reader:   r,
		Registry: registry,
		scheme:   scheme,
		encoder:  encoder,
	}
}

type localStorageBackedClient struct {
	client.Reader
	Registry *ociartifact.Registry
	scheme   *runtime.Scheme
	encoder  runtime.Encoder
}

var _ Client = &localStorageBackedClient{}

func (clnt *localStorageBackedClient) Scheme() *runtime.Scheme {
	return clnt.scheme
}

func (clnt *localStorageBackedClient) GetTarget(ctx context.Context, ref v1alpha1.ConfigurationReference) (target types.ConfigurationTarget, err error) {
	switch ref.Kind {
	case v1alpha1.KindConfiguredResource:
		fallthrough
	case v1alpha1.KindLocalizedResource:
		fallthrough
	case v1alpha1.KindResource:
		return ociartifact.GetContentBackedByArtifactFromComponent(ctx, clnt.Reader, clnt.Registry, &ref)
	default:
		return nil, fmt.Errorf("unsupported configuration target kind: %s", ref.Kind)
	}
}

func (clnt *localStorageBackedClient) GetConfiguration(ctx context.Context, ref v1alpha1.ConfigurationReference) (source types.ConfigurationSource, err error) {
	switch ref.Kind {
	case v1alpha1.KindResource:
		return ociartifact.GetContentBackedByArtifactFromComponent(ctx, clnt.Reader, clnt.Registry, &ref)
	case v1alpha1.KindResourceConfig:
		return GetResourceConfigFromKubernetes(ctx, clnt.Reader, clnt.encoder, ref)
	default:
		return nil, fmt.Errorf("unsupported configuration source kind: %s", ref.Kind)
	}
}

func GetResourceConfigFromKubernetes(ctx context.Context, clnt client.Reader, encoder runtime.Encoder, reference v1alpha1.ConfigurationReference) (types.ConfigurationSource, error) {
	if reference.APIVersion == "" {
		reference.APIVersion = v1alpha1.GroupVersion.String()
	}
	if reference.APIVersion != v1alpha1.GroupVersion.String() || reference.Kind != "ResourceConfig" {
		return nil, fmt.Errorf("unsupported localization target reference: %s/%s", reference.APIVersion, reference.Kind)
	}

	cfg := v1alpha1.ResourceConfig{}
	if err := clnt.Get(ctx, client.ObjectKey{
		Namespace: reference.Namespace,
		Name:      reference.Name,
	}, &cfg); err != nil {
		return nil, fmt.Errorf("failed to fetch localization config %s: %w", reference.Name, err)
	}

	return &ociartifact.ObjectConfig{Object: &cfg, Encoder: encoder}, nil
}

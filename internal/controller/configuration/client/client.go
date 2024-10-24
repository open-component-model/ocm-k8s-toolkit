package client

import (
	"context"
	"fmt"

	"github.com/openfluxcd/controller-manager/storage"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/configuration/types"
	artifactutil "github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"
)

type Client interface {
	client.Reader

	// Scheme returns the scheme this client is using.
	Scheme() *runtime.Scheme

	GetConfigurationSource(ctx context.Context, ref v1alpha1.ConfigurationReference) (source types.ConfigurationSource, err error)
	GetConfigurationTarget(ctx context.Context, ref v1alpha1.ConfigurationReference) (target types.ConfigurationTarget, err error)
}

func NewClientWithLocalStorage(r client.Reader, s *storage.Storage, scheme *runtime.Scheme) Client {
	return &localStorageBackedClient{
		Reader:  r,
		Storage: s,
		scheme:  scheme,
	}
}

type localStorageBackedClient struct {
	client.Reader
	*storage.Storage
	scheme *runtime.Scheme
}

var _ Client = &localStorageBackedClient{}

func (clnt *localStorageBackedClient) Scheme() *runtime.Scheme {
	return clnt.scheme
}

func (clnt *localStorageBackedClient) GetConfigurationTarget(ctx context.Context, ref v1alpha1.ConfigurationReference) (target types.ConfigurationTarget, err error) {
	switch ref.Kind {
	case "Resource":
		return artifactutil.GetContentBackedByStorageAndResource(ctx, clnt.Reader, clnt.Storage, ref.NamespacedObjectKindReference)
	default:
		return nil, fmt.Errorf("unsupported configuration target kind: %s", ref.Kind)
	}
}

func (clnt *localStorageBackedClient) GetConfigurationSource(ctx context.Context, ref v1alpha1.ConfigurationReference) (source types.ConfigurationSource, err error) {
	switch ref.Kind {
	case "Resource":
		return artifactutil.GetContentBackedByStorageAndResource(ctx, clnt.Reader, clnt.Storage, ref.NamespacedObjectKindReference)
	default:
		return nil, fmt.Errorf("unsupported configuration source kind: %s", ref.Kind)
	}
}

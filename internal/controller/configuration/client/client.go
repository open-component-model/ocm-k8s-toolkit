package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/opencontainers/go-digest"
	"github.com/openfluxcd/controller-manager/storage"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/configuration/types"
	artifactutil "github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"
)

type Client interface {
	client.Reader

	// Scheme returns the scheme this client is using.
	Scheme() *runtime.Scheme

	GetConfiguration(ctx context.Context, ref v1alpha1.ConfigurationReference) (source types.ConfigurationSource, err error)
	GetTarget(ctx context.Context, ref v1alpha1.ConfigurationReference) (target types.ConfigurationTarget, err error)
}

func NewClientWithLocalStorage(r client.Reader, s *storage.Storage, scheme *runtime.Scheme) Client {
	factory := serializer.NewCodecFactory(scheme)
	info, _ := runtime.SerializerInfoForMediaType(factory.SupportedMediaTypes(), runtime.ContentTypeYAML)
	encoder := factory.EncoderForVersion(info.Serializer, v1alpha1.GroupVersion)

	return &localStorageBackedClient{
		Reader:  r,
		Storage: s,
		scheme:  scheme,
		encoder: encoder,
	}
}

type localStorageBackedClient struct {
	client.Reader
	*storage.Storage
	scheme  *runtime.Scheme
	encoder runtime.Encoder
}

var _ Client = &localStorageBackedClient{}

func (clnt *localStorageBackedClient) Scheme() *runtime.Scheme {
	return clnt.scheme
}

func (clnt *localStorageBackedClient) GetTarget(ctx context.Context, ref v1alpha1.ConfigurationReference) (target types.ConfigurationTarget, err error) {
	switch ref.Kind {
	case "Resource":
		return artifactutil.GetContentBackedByArtifactFromComponent(ctx, clnt.Reader, clnt.Storage, ref.NamespacedObjectKindReference)
	default:
		return nil, fmt.Errorf("unsupported configuration target kind: %s", ref.Kind)
	}
}

func (clnt *localStorageBackedClient) GetConfiguration(ctx context.Context, ref v1alpha1.ConfigurationReference) (source types.ConfigurationSource, err error) {
	switch ref.Kind {
	case "Resource":
		return artifactutil.GetContentBackedByArtifactFromComponent(ctx, clnt.Reader, clnt.Storage, ref.NamespacedObjectKindReference)
	case "ResourceConfig":
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

	return &ResourceConfig{&cfg, encoder}, nil
}

// ResourceConfig is a wrapper around the v1alpha1.ResourceConfig that implements the types.ConfigurationSource interface.
// For serialization it uses the provided runtime.Encoder.
type ResourceConfig struct {
	*v1alpha1.ResourceConfig `json:",inline"`
	encoder                  runtime.Encoder
}

var _ types.ConfigurationSource = &ResourceConfig{}

func (in *ResourceConfig) Open() (io.ReadCloser, error) {
	buf, err := in.AsBuf()
	if err != nil {
		return nil, err
	}

	return io.NopCloser(buf), nil
}

func (in *ResourceConfig) AsBuf() (*bytes.Buffer, error) {
	var buf bytes.Buffer

	if err := in.encoder.Encode(in.ResourceConfig, &buf); err != nil {
		return nil, err
	}

	return &buf, nil
}

func (in *ResourceConfig) UnpackIntoDirectory(path string) error {
	buf, err := in.AsBuf()
	if err != nil {
		return err
	}

	return os.WriteFile(fmt.Sprintf("%s-%s.yaml", path, in.Name), buf.Bytes(), 0o600)
}

func (in *ResourceConfig) GetDigest() (string, error) {
	buf, err := in.AsBuf()
	if err != nil {
		return "", err
	}

	return digest.NewDigestFromBytes(digest.SHA256, buf.Bytes()).String(), err
}

func (in *ResourceConfig) GetRevision() string {
	return fmt.Sprintf("%s/%s in generation %v", in.GetNamespace(), in.GetName(), in.GetGeneration())
}

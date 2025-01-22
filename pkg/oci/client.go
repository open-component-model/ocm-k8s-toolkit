package oci

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	corev1 "k8s.io/api/core/v1"
	apitypes "k8s.io/apimachinery/pkg/types"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// ClientOptsFunc options are used to leave the cache backwards compatible.
// If the certificate isn't defined, we will use `WithInsecure`.
type ClientOptsFunc func(opts *Client)

// WithCertificateSecret defines the name of the secret holding the certificates.
func WithCertificateSecret(name string) ClientOptsFunc {
	return func(opts *Client) {
		opts.CertSecretName = name
	}
}

// WithNamespace sets up certificates for the client.
func WithNamespace(namespace string) ClientOptsFunc {
	return func(opts *Client) {
		opts.Namespace = namespace
	}
}

// WithInsecureSkipVerify sets up certificates for the client.
func WithInsecureSkipVerify(value bool) ClientOptsFunc {
	return func(opts *Client) {
		opts.InsecureSkipVerify = value
	}
}

// WithClient sets up certificates for the client.
func WithClient(client client.Client) ClientOptsFunc {
	return func(opts *Client) {
		opts.Client = client
	}
}

// Client implements the caching layer and the OCI layer.
type Client struct {
	Client             client.Client
	OCIRepositoryAddr  string
	InsecureSkipVerify bool
	Namespace          string
	CertSecretName     string

	certPem []byte
	keyPem  []byte
	ca      []byte
}

// WithTransport sets up insecure TLS so the library is forced to use HTTPS.
func (c *Client) WithTransport(ctx context.Context) Option {
	return func(o *options) error {
		if c.InsecureSkipVerify {
			return nil
		}

		if c.certPem == nil && c.keyPem == nil {
			if err := c.setupCertificates(ctx); err != nil {
				return fmt.Errorf("failed to set up certificates for transport: %w", err)
			}
		}

		o.remoteOpts = append(o.remoteOpts, remote.WithTransport(c.constructTLSRoundTripper()))

		return nil
	}
}

func (c *Client) setupCertificates(ctx context.Context) error {
	if c.Client == nil {
		return fmt.Errorf("client must not be nil if certificate is requested, please set WithClient when creating the oci cache")
	}
	registryCerts := &corev1.Secret{}
	if err := c.Client.Get(ctx, apitypes.NamespacedName{Name: c.CertSecretName, Namespace: c.Namespace}, registryCerts); err != nil {
		return fmt.Errorf("unable to find the secret containing the registry certificates: %w", err)
	}

	certFile, ok := registryCerts.Data["tls.crt"]
	if !ok {
		return fmt.Errorf("tls.crt data not found in registry certificate secret")
	}

	keyFile, ok := registryCerts.Data["tls.key"]
	if !ok {
		return fmt.Errorf("tls.key data not found in registry certificate secret")
	}

	caFile, ok := registryCerts.Data["ca.crt"]
	if !ok {
		return fmt.Errorf("ca.crt data not found in registry certificate secret")
	}

	c.certPem = certFile
	c.keyPem = keyFile
	c.ca = caFile

	return nil
}

func (c *Client) constructTLSRoundTripper() http.RoundTripper {
	tlsConfig := &tls.Config{} //nolint:gosec // must provide lower version for quay.io
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(c.ca)

	tlsConfig.Certificates = []tls.Certificate{
		{
			Certificate: [][]byte{c.certPem},
			PrivateKey:  c.keyPem,
		},
	}

	tlsConfig.RootCAs = caCertPool
	tlsConfig.InsecureSkipVerify = c.InsecureSkipVerify

	// Create a new HTTP transport with the TLS configuration
	return &http.Transport{
		TLSClientConfig: tlsConfig,
	}
}

// NewClient creates a new OCI Client.
func NewClient(ociAddress string, opts ...ClientOptsFunc) *Client {
	c := &Client{
		OCIRepositoryAddr: ociAddress,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// PushData takes a blob of data and caches it using OCI as a background.
func (c *Client) PushData(ctx context.Context, data io.ReadCloser, mediaType, name, tag string) (string, int64, error) {
	repositoryName := fmt.Sprintf("%s/%s", c.OCIRepositoryAddr, name)
	repo, err := NewRepository(repositoryName, c.WithTransport(ctx))
	if err != nil {
		return "", -1, fmt.Errorf("failed create new repository: %w", err)
	}

	manifest, err := repo.PushStreamingImage(tag, data, mediaType, nil)
	if err != nil {
		return "", -1, fmt.Errorf("failed to push image: %w", err)
	}

	layers := manifest.Layers
	if len(layers) == 0 {
		return "", -1, fmt.Errorf("no layers returned by manifest: %w", err)
	}

	return layers[0].Digest.String(), layers[0].Size, nil
}

// FetchDataByIdentity fetches an existing resource. Errors if there is no resource available. It's advised to call IsCached
// before fetching. Returns the digest of the resource alongside the data for further processing.
func (c *Client) FetchDataByIdentity(ctx context.Context, name, tag string) (io.ReadCloser, string, int64, error) {
	logger := log.FromContext(ctx).WithName("cache")
	repositoryName := fmt.Sprintf("%s/%s", c.OCIRepositoryAddr, name)
	logger.V(v1alpha1.LevelDebug).Info("cache hit for data", "name", name, "tag", tag, "repository", repositoryName)
	repo, err := NewRepository(repositoryName, c.WithTransport(ctx))
	if err != nil {
		return nil, "", -1, fmt.Errorf("failed to get repository: %w", err)
	}

	manifest, _, err := repo.FetchManifest(tag, nil)
	if err != nil {
		return nil, "", -1, fmt.Errorf("failed to fetch manifest to obtain layers: %w", err)
	}
	logger.V(v1alpha1.LevelDebug).Info("got the manifest", "manifest", manifest)
	layers := manifest.Layers
	if len(layers) == 0 {
		return nil, "", -1, fmt.Errorf("layers for repository is empty")
	}

	digest := layers[0].Digest

	reader, err := repo.FetchBlob(digest.String())
	if err != nil {
		return nil, "", -1, fmt.Errorf("failed to fetch reader for digest of the 0th layer: %w", err)
	}

	// decompresses the data coming from the cache. Because a streaming layer doesn't support decompression
	// and a static layer returns the data AS IS, we have to decompress it ourselves.
	return reader, digest.String(), layers[0].Size, nil
}

// FetchDataByDigest returns a reader for a given digest.
func (c *Client) FetchDataByDigest(ctx context.Context, name, digest string) (io.ReadCloser, error) {
	repositoryName := fmt.Sprintf("%s/%s", c.OCIRepositoryAddr, name)

	repo, err := NewRepository(repositoryName, c.WithTransport(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}

	reader, err := repo.FetchBlob(digest)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch blob: %w", err)
	}

	// decompresses the data coming from the cache. Because a streaming layer doesn't support decompression
	// and a static layer returns the data AS IS, we have to decompress it ourselves.
	return reader, nil
}

// IsCached returns whether a certain tag with a given name exists in cache.
func (c *Client) IsCached(ctx context.Context, name, tag string) (bool, error) {
	repositoryName := fmt.Sprintf("%s/%s", c.OCIRepositoryAddr, name)

	repo, err := NewRepository(repositoryName, c.WithTransport(ctx))
	if err != nil {
		return false, fmt.Errorf("failed to get repository: %w", err)
	}

	return repo.head(tag)
}

// DeleteData removes a specific tag from the cache.
func (c *Client) DeleteData(ctx context.Context, name, tag string) error {
	repositoryName := fmt.Sprintf("%s/%s", c.OCIRepositoryAddr, name)
	repo, err := NewRepository(repositoryName, c.WithTransport(ctx))
	if err != nil {
		return fmt.Errorf("failed create new repository: %w", err)
	}

	return repo.deleteTag(tag)
}

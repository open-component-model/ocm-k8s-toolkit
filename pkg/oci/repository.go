package oci

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/opencontainers/go-digest"
	"helm.sh/helm/v3/pkg/registry"

	ociname "github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// Option is a functional option for Repository.
type Option func(o *options) error

type options struct {
	// remoteOpts are the options to use when fetching and pushing blobs.
	remoteOpts []remote.Option
}

// ResourceOptions contains all parameters necessary to fetch / push resources.
type ResourceOptions struct {
	// TODO: Check for replacement
	// ComponentVersion *v1alpha1.ComponentVersion
	Resource     v1alpha1.ResourceReference
	Owner        metav1.Object
	SnapshotName string
}

// Repository is a wrapper around go-container registry's name.Repository.
// It provides a few convenience methods for interacting with OCI registries.
type Repository struct {
	ociname.Repository
	options
}

// NewRepository returns a new Repository. It points to the given remote repository.
// It accepts a list of options to configure the repository and the underlying remote client.
func NewRepository(repositoryName string, opts ...Option) (*Repository, error) {
	opt, err := makeOptions(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to make options: %w", err)
	}
	repoOpts := make([]ociname.Option, 0)

	repo, err := ociname.NewRepository(repositoryName, repoOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Repository name %q: %w", repositoryName, err)
	}

	return &Repository{repo, opt}, nil
}

// head does an authenticated call with the repo context to see if a tag in a repository already exists or not.
func (r *Repository) head(tag string) (bool, error) {
	reference, err := ociname.ParseReference(fmt.Sprintf("%s:%s", r.Repository, tag))
	if err != nil {
		return false, fmt.Errorf("failed to parse repository and tag name: %w", err)
	}

	if _, err := remote.Head(reference, r.remoteOpts...); err != nil {
		terr := &transport.Error{}
		if ok := errors.As(err, &terr); ok {
			if terr.StatusCode == http.StatusNotFound {
				return false, nil
			}
		}

		return false, err
	}

	return true, nil
}

// deleteTag fetches the latest digest for a tag. This will delete the whole Manifest.
// This is done because docker registry doesn't technically support deleting a single Tag.
// But since we have a 1:1 relationship between a tag and a manifest, it's safe to delete
// the complete manifest.
func (r *Repository) deleteTag(tag string) error {
	ref, err := ociname.NewTag(fmt.Sprintf("%s:%s", r.Repository, tag))
	if err != nil {
		return fmt.Errorf("failed to parse reference: %w", err)
	}
	desc, err := remote.Head(ref, r.remoteOpts...)
	if err != nil {
		return fmt.Errorf("failed to fetch head for reference: %w", err)
	}

	deleteRef, err := parseReference(desc.Digest.String(), r)
	if err != nil {
		return fmt.Errorf("failed to construct reference for calculated digest: %w", err)
	}

	if err := remote.Delete(deleteRef, r.remoteOpts...); err != nil {
		return fmt.Errorf("failed to delete ref '%s': %w", ref, err)
	}

	return nil
}

// fetchBlob fetches a blob from the repository.
func (r *Repository) fetchBlob(digest string) (v1.Layer, error) {
	ref, err := ociname.NewDigest(fmt.Sprintf("%s@%s", r.Repository, digest))
	if err != nil {
		return nil, fmt.Errorf("failed to parse digest %q: %w", digest, err)
	}

	return remote.Layer(ref, r.remoteOpts...)
}

// FetchBlob fetches a blob from the repository.
func (r *Repository) FetchBlob(digest string) (io.ReadCloser, error) {
	l, err := r.fetchBlob(digest)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch layer: %w", err)
	}

	return l.Uncompressed()
}

// PushStreamBlob pushes by streaming a blob to the repository. It accepts an io.ReadCloser interface.
// A media type can be specified to override the default media type.
// Default media type is "application/vnd.oci.image.layer.v1.tar+gzip".
func (r *Repository) PushStreamBlob(blob io.ReadCloser, mediaType string) (*ocispec.Descriptor, error) {
	t := types.MediaType(mediaType)
	if t == "" {
		t = types.OCILayer
	}
	layer := stream.NewLayer(blob, stream.WithMediaType(t))
	err := r.pushBlob(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to push layer: %w", err)
	}
	desc, err := layerToOCIDescriptor(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get layer descriptor: %w", err)
	}

	return desc, nil
}

// pushBlob pushes a blob to the repository. It accepts a v1.Layer interface.
func (r *Repository) pushBlob(layer v1.Layer) error {
	return remote.WriteLayer(r.Repository, layer, r.remoteOpts...)
}

// PushStreamingImage pushes a reader to the repository as a streaming OCI image.
// It accepts a media type and a byte slice as the blob.
// Default media type is "application/vnd.oci.image.layer.v1.tar+gzip".
// Annotations can be passed to the image manifest.
func (r *Repository) PushStreamingImage(
	reference string,
	reader io.ReadCloser,
	mediaType string,
	annotations map[string]string,
) (*v1.Manifest, error) {
	ref, err := parseReference(reference, r)
	if err != nil {
		return nil, fmt.Errorf("failed to parse reference: %w", err)
	}
	image, err := computeStreamImage(reader, mediaType)
	if err != nil {
		return nil, fmt.Errorf("failed to compute image: %w", err)
	}
	if len(annotations) > 0 {
		i, ok := mutate.Annotations(image, annotations).(v1.Image)
		if !ok {
			return nil, fmt.Errorf("returned object was not an Image")
		}

		image = i
	}

	// These MediaTypes are required to create a Helm compliant OCI repository.
	if mediaType == registry.ChartLayerMediaType {
		image = mutate.ConfigMediaType(image, registry.ConfigMediaType)
		image = mutate.MediaType(image, ocispec.MediaTypeImageManifest)
	}

	if err := r.pushImage(image, ref); err != nil {
		return nil, fmt.Errorf("failed to push image: %w", err)
	}

	return image.Manifest()
}

// pushImage pushes an OCI image to the repository. It accepts a v1.RepositoryURL interface.
func (r *Repository) pushImage(image v1.Image, reference ociname.Reference) error {
	return remote.Write(reference, image, r.remoteOpts...)
}

// FetchManifest fetches a manifest from the repository.
// It returns the manifest as an oci.Manifest struct and the raw manifest as a byte slice.
// The oci.Manifest struct can be used to retrieve the layers digests.
// Optionally, the manifest annotations can be verified against the given slice of strings keys.
func (r *Repository) FetchManifest(reference string, filters []string) (*ocispec.Manifest, []byte, error) {
	ref, err := parseReference(reference, r)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse reference: %w", err)
	}
	m, err := r.fetchManifestDescriptor(ref.String())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch manifest: %w", err)
	}
	raw, err := m.RawManifest()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get raw manifest: %w", err)
	}

	// check if the manifest annotations match the given filters
	var annotations map[string]string
	if len(filters) > 0 {
		// get descriptor from manifest
		desc, err := getDescriptor(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get descriptor: %w", err)
		}
		annotations = filterAnnotations(desc.Annotations, filters)
		if len(annotations) == 0 {
			return nil, nil, fmt.Errorf("no matching annotations found")
		}
	}

	desc, err := manifestToOCIDescriptor(m)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get manifest descriptor: %w", err)
	}

	return desc, raw, nil
}

func (r *Repository) fetchManifestDescriptor(s string) (*remote.Descriptor, error) {
	return fetchManifestDescriptorFrom(s, r.remoteOpts...)
}

// manifestToOCIDescriptor converts a manifest to an OCI Manifest struct.
// It contains the layers descriptors.
func manifestToOCIDescriptor(m *remote.Descriptor) (*ocispec.Manifest, error) {
	ociManifest := &ocispec.Manifest{}
	ociManifest.MediaType = string(m.MediaType)
	image, err := m.Image()
	if err != nil {
		return nil, fmt.Errorf("failed to get image: %w", err)
	}
	layers, err := image.Layers()
	if err != nil {
		return nil, fmt.Errorf("failed to get layers: %w", err)
	}
	for _, layer := range layers {
		ociLayer, err := layerToOCIDescriptor(layer)
		if err != nil {
			return nil, fmt.Errorf("failed to get layer: %w", err)
		}
		ociManifest.Layers = append(ociManifest.Layers, *ociLayer)
	}

	return ociManifest, nil
}

func fetchManifestDescriptorFrom(s string, opts ...remote.Option) (*remote.Descriptor, error) {
	// a manifest reference can be a tag or a digest
	ref, err := ociname.ParseReference(s)
	if err != nil {
		return nil, fmt.Errorf("failed to parse reference: %w", err)
	}
	// fetch manifest
	// Get performs a digest verification
	return remote.Get(ref, opts...)
}

func parseReference(reference string, r *Repository) (ociname.Reference, error) {
	if reference == "" {
		return nil, fmt.Errorf("reference must be specified")
	}
	if strings.Contains(reference, "sha256:") {
		reference = fmt.Sprintf("%s@%s", r.Repository, reference)
	} else {
		reference = fmt.Sprintf("%s:%s", r.Repository, reference)
	}
	ref, err := ociname.ParseReference(reference)
	if err != nil {
		return nil, fmt.Errorf("failed to parse reference: %w", err)
	}

	return ref, nil
}

// layerToOCIDescriptor converts a layer to an OCI Layer struct.
func layerToOCIDescriptor(layer v1.Layer) (*ocispec.Descriptor, error) {
	ociLayer := &ocispec.Descriptor{}
	mediaType, err := layer.MediaType()
	if err != nil {
		return nil, fmt.Errorf("failed to get media type: %w", err)
	}
	d, err := layer.Digest()
	if err != nil {
		return nil, fmt.Errorf("failed to get digest: %w", err)
	}
	size, err := layer.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get size: %w", err)
	}
	ociLayer.MediaType = string(mediaType)
	ociLayer.Digest = digest.NewDigestFromHex(d.Algorithm, d.Hex)
	ociLayer.Size = size

	return ociLayer, nil
}

func makeOptions(opts ...Option) (options, error) {
	opt := options{}
	for _, o := range opts {
		if err := o(&opt); err != nil {
			return options{}, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	return opt, nil
}

// filterAnnotations filters the annotations of a map of annotations.
// It returns a map of annotations that match the given entries.
func filterAnnotations(annotations map[string]string, filters []string) map[string]string {
	filtered := make(map[string]string)
	for k, v := range annotations {
		for _, match := range filters {
			if strings.EqualFold(k, match) {
				filtered[k] = v
			}
		}
	}

	return filtered
}

func computeStreamImage(reader io.ReadCloser, mediaType string) (v1.Image, error) {
	return mutate.AppendLayers(empty.Image, computeStreamBlob(reader, mediaType))
}

func computeStreamBlob(reader io.ReadCloser, mediaType string) v1.Layer {
	t := types.MediaType(mediaType)
	if t == "" {
		t = types.OCILayer
	}

	return stream.NewLayer(reader, stream.WithMediaType(t))
}

func getDescriptor(manifest []byte) (*v1.Descriptor, error) {
	desc := &v1.Descriptor{}
	if err := json.Unmarshal(manifest, desc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest: %w", err)
	}

	return desc, nil
}

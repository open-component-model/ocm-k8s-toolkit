package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mitchellh/hashstructure/v2"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ociV1 "github.com/opencontainers/image-spec/specs-go/v1"
)

const OCISchemaVersion = 2

// A RepositoryType is a type that can push and fetch blobs.
type RepositoryType interface {
	// PushSnapshot pushes the blob to its repository. It returns the manifest-digest to retrieve the blob.
	PushSnapshot(ctx context.Context, reference string, blob []byte) (digest.Digest, error)

	FetchSnapshot(ctx context.Context, reference string) (io.ReadCloser, error)

	DeleteSnapshot(ctx context.Context, digest string) error
}

type Repository struct {
	*remote.Repository
}

func (r *Repository) PushSnapshot(ctx context.Context, tag string, blob []byte) (digest.Digest, error) {
	logger := log.FromContext(ctx)

	// Prepare and upload blob
	blobDescriptor := ociV1.Descriptor{
		MediaType: ociV1.MediaTypeImageLayer,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	logger.Info("pushing blob", "descriptor", blobDescriptor)
	if err := r.Push(ctx, blobDescriptor, content.NewVerifyReader(
		bytes.NewReader(blob),
		blobDescriptor,
	)); err != nil {
		return "", fmt.Errorf("oci: error pushing blob: %w", err)
	}

	// Prepare and upload image config
	emptyImageConfig := []byte("{}")

	imageConfigDescriptor := ociV1.Descriptor{
		MediaType: ociV1.MediaTypeImageConfig,
		Digest:    digest.FromBytes(emptyImageConfig),
		Size:      int64(len(emptyImageConfig)),
	}

	logger.Info("pushing OCI config")
	if err := r.Push(ctx, imageConfigDescriptor, content.NewVerifyReader(
		bytes.NewReader(emptyImageConfig),
		imageConfigDescriptor,
	)); err != nil {
		return "", fmt.Errorf("oci: error pushing empty config: %w", err)
	}

	// Prepare and upload manifest
	manifest := ociV1.Manifest{
		Versioned: specs.Versioned{SchemaVersion: OCISchemaVersion},
		MediaType: ociV1.MediaTypeImageManifest,
		Config:    imageConfigDescriptor,
		Layers:    []ociV1.Descriptor{blobDescriptor},
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("oci: error marshaling manifest: %w", err)
	}

	manifestDigest := digest.FromBytes(manifestBytes)

	manifestDescriptor := ociV1.Descriptor{
		MediaType: manifest.MediaType,
		Digest:    manifestDigest,
		Size:      int64(len(manifestBytes)),
	}

	logger.Info("pushing OCI manifest")
	if err := r.Push(ctx, manifestDescriptor, content.NewVerifyReader(
		bytes.NewReader(manifestBytes),
		manifestDescriptor,
	)); err != nil {
		return "", fmt.Errorf("oci: error pushing manifest: %w", err)
	}

	logger.Info("tagging OCI manifest")
	if err := r.Tag(ctx, manifestDescriptor, tag); err != nil {
		return "", fmt.Errorf("oci: error tagging manifest: %w", err)
	}

	logger.Info("finished pushing snapshot")

	return manifestDigest, nil
}

func (r *Repository) FetchSnapshot(ctx context.Context, manifestDigest string) (io.ReadCloser, error) {
	// Fetch manifest descriptor to get manifest.
	manifestDescriptor, _, err := r.FetchReference(ctx, manifestDigest)
	if err != nil {
		return nil, fmt.Errorf("oci: error fetching manifest: %w", err)
	}

	// Fetch manifest to get layer[0] descriptor.
	manifestReader, err := r.Fetch(ctx, manifestDescriptor)
	if err != nil {
		return nil, fmt.Errorf("oci: error fetching manifest: %w", err)
	}

	var manifest ociV1.Manifest
	if err := json.NewDecoder(manifestReader).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("oci: error parsing manifest: %w", err)
	}

	// We only expect single layer artifacts.
	if len(manifest.Layers) != 1 {
		return nil, fmt.Errorf("oci: expected 1 layer, got %d", len(manifest.Layers))
	}

	return r.Fetch(ctx, manifest.Layers[0])
}

func (r *Repository) DeleteSnapshot(ctx context.Context, manifestDigest string) error {
	manifestDescriptor, _, err := r.FetchReference(ctx, manifestDigest)
	if err != nil {
		return fmt.Errorf("oci: error fetching manifest: %w", err)
	}

	return r.Delete(ctx, manifestDescriptor)
}

// CreateRepositoryName creates a name for an OCI repository and returns a hashed string from the passed arguments. The
// purpose of this function is to sanitize any passed string to an OCI repository compliant name.
func CreateRepositoryName(args ...string) (string, error) {
	hash, err := hashstructure.Hash(args, hashstructure.FormatV2, nil)
	if err != nil {
		return "", fmt.Errorf("failed to hash identity: %w", err)
	}

	return fmt.Sprintf("sha-%d", hash), nil
}

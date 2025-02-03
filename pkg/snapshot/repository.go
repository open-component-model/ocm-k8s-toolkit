package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

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

	FetchSnapshot(ctx context.Context, reference string) ([]byte, error)

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

	// Tag manifest
	if err := r.Tag(ctx, manifestDescriptor, tag); err != nil {
		return "", fmt.Errorf("oci: error tagging manifest: %w", err)
	}

	return manifestDigest, nil
}

func (r *Repository) FetchSnapshot(_ context.Context, _ string) ([]byte, error) {
	return []byte{}, nil
}

func (r *Repository) DeleteSnapshot(ctx context.Context, digestString string) error {
	manifestDescriptor, _, err := r.FetchReference(ctx, digestString)
	if err != nil {
		return fmt.Errorf("error fetching manifest: %w", err)
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

package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ociV1 "github.com/opencontainers/image-spec/specs-go/v1"
	ocmctx "ocm.software/ocm/api/ocm"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// A RepositoryType is a type that can push and fetch blobs.
type RepositoryType interface {
	// PushSnapshot is a wrapper to push a single layer OCI artifact with an empty config and a single data layer
	// containing the blob. As all snapshots are produced and consumed by us, we do not have to care about the
	// configuration.
	PushSnapshot(ctx context.Context, reference string, blob []byte) (digest.Digest, error)

	// FetchSnapshot is a wrapper to fetch a single layer OCI artifact with a manifest digest. It expects and returns
	// the single data layer.
	FetchSnapshot(ctx context.Context, reference string) ([]byte, error)

	// DeleteSnapshot is a wrapper to delete a single layer OCI artifact with a manifest digest.
	DeleteSnapshot(ctx context.Context, digest string) error

	// ExistsSnapshot is a wrapper to check if an OCI repository exists using the manifest digest.
	ExistsSnapshot(ctx context.Context, manifestDigest string) (bool, error)

	// CopyOCIArtifactForResourceAccess is a wrapper to copy an OCI artifact from an OCM resource access.
	CopyOCIArtifactForResourceAccess(ctx context.Context, access ocmctx.ResourceAccess) (digest.Digest, error)

	GetHost() string
	GetName() string
}

// Repository is a wrapper for an OCI repository to provide snapshot methods.
type Repository struct {
	*remote.Repository
}

func (r *Repository) GetHost() string {
	return r.Reference.Host()
}

func (r *Repository) GetName() string {
	return r.Reference.Repository
}

func (r *Repository) PushSnapshot(ctx context.Context, tag string, blob []byte) (digest.Digest, error) {
	logger := log.FromContext(ctx)

	// Prepare and upload blob
	blobDescriptor := ociV1.Descriptor{
		// The media type is meaningless as we do not use it. Thus, we just set a default one.
		MediaType: ociV1.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	logger.Info("pushing blob", "descriptor", blobDescriptor)
	if err := r.Push(ctx, blobDescriptor, content.NewVerifyReader(
		bytes.NewReader(blob),
		blobDescriptor,
	)); err != nil {
		return "", fmt.Errorf("error pushing blob: %w", err)
	}

	// Prepare and upload an empty image config. As we do not plan to make use of the image config, the content does
	// not matter.
	emptyImageConfig := []byte("{}")

	imageConfigDescriptor := ociV1.Descriptor{
		MediaType: ociV1.MediaTypeImageConfig,
		Digest:    digest.FromBytes(emptyImageConfig),
		Size:      int64(len(emptyImageConfig)),
	}

	logger.Info("pushing empty image config", "descriptor", imageConfigDescriptor)
	if err := r.Push(ctx, imageConfigDescriptor, content.NewVerifyReader(
		bytes.NewReader(emptyImageConfig),
		imageConfigDescriptor,
	)); err != nil {
		return "", fmt.Errorf("error pushing empty config: %w", err)
	}

	// Prepare and upload manifest
	manifest := ociV1.Manifest{
		Versioned: specs.Versioned{SchemaVersion: v1alpha1.OCISchemaVersion},
		MediaType: ociV1.MediaTypeImageManifest,
		Config:    imageConfigDescriptor,
		Layers:    []ociV1.Descriptor{blobDescriptor},
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("error marshaling manifest: %w", err)
	}

	manifestDigest := digest.FromBytes(manifestBytes)

	manifestDescriptor := ociV1.Descriptor{
		MediaType: manifest.MediaType,
		Digest:    manifestDigest,
		Size:      int64(len(manifestBytes)),
	}

	logger.Info("pushing image manifest", "descriptor", manifestDescriptor)
	if err := r.Push(ctx, manifestDescriptor, content.NewVerifyReader(
		bytes.NewReader(manifestBytes),
		manifestDescriptor,
	)); err != nil {
		return "", fmt.Errorf("error pushing manifest: %w", err)
	}

	logger.Info("tagging image manifest", "tag", tag)
	if err := r.Tag(ctx, manifestDescriptor, tag); err != nil {
		return "", fmt.Errorf("error tagging manifest: %w", err)
	}

	logger.Info("pushed single OCI artifact")

	return manifestDigest, nil
}

func (r *Repository) FetchSnapshot(ctx context.Context, manifestDigest string) ([]byte, error) {
	// Fetch manifest descriptor to get manifest.
	manifestDescriptor, _, err := r.FetchReference(ctx, manifestDigest)
	if err != nil {
		return nil, fmt.Errorf("error fetching manifest: %w", err)
	}

	manifestReader, err := r.Fetch(ctx, manifestDescriptor)
	if err != nil {
		return nil, fmt.Errorf("error fetching manifest: %w", err)
	}

	var manifest ociV1.Manifest
	if err := json.NewDecoder(manifestReader).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("error parsing manifest: %w", err)
	}

	// We only expect single layer artifacts.
	if len(manifest.Layers) != 1 {
		return nil, fmt.Errorf("expected 1 layer, got %d", len(manifest.Layers))
	}

	reader, err := r.Fetch(ctx, manifest.Layers[0])
	if err != nil {
		return nil, fmt.Errorf("error fetching layer: %w", err)
	}

	return io.ReadAll(reader)
}

func (r *Repository) DeleteSnapshot(ctx context.Context, manifestDigest string) error {
	manifestDescriptor, _, err := r.FetchReference(ctx, manifestDigest)
	if err != nil {
		return fmt.Errorf("error fetching manifest: %w", err)
	}

	return r.Delete(ctx, manifestDescriptor)
}

func (r *Repository) ExistsSnapshot(ctx context.Context, manifestDigest string) (bool, error) {
	manifestDescriptor, _, err := r.FetchReference(ctx, manifestDigest)
	if err != nil {
		return false, fmt.Errorf("error fetching manifest: %w", err)
	}

	return r.Exists(ctx, manifestDescriptor)
}

func (r *Repository) CopyOCIArtifactForResourceAccess(ctx context.Context, access ocmctx.ResourceAccess) (digest.Digest, error) {
	logger := log.FromContext(ctx)

	gloAccess := access.GlobalAccess()
	accessSpec, ok := gloAccess.(*ociartifact.AccessSpec)
	if !ok {
		return "", fmt.Errorf("expected type ociartifact.AccessSpec, but got %T", gloAccess)
	}

	var http bool
	var refSanitized string
	refURL, err := url.Parse(accessSpec.ImageReference)
	if err != nil {
		return "", fmt.Errorf("error parsing image reference: %w", err)
	}

	if refURL.Scheme != "" {
		if refURL.Scheme == "http" {
			http = true
		}
		refSanitized = strings.TrimPrefix(accessSpec.ImageReference, refURL.Scheme+"://")
	} else {
		refSanitized = accessSpec.ImageReference
	}

	ref, err := name.ParseReference(refSanitized)
	if err != nil {
		return "", fmt.Errorf("error parsing image reference: %w", err)
	}

	sourceRegistry, err := remote.NewRegistry(ref.Context().RegistryStr())
	if err != nil {
		return "", fmt.Errorf("error creating source registry: %w", err)
	}

	if http {
		sourceRegistry.PlainHTTP = true
	}

	sourceRepository, err := sourceRegistry.Repository(ctx, ref.Context().RepositoryStr())
	if err != nil {
		return "", fmt.Errorf("error creating source repository: %w", err)
	}

	desc, err := oras.Copy(ctx, sourceRepository, ref.Identifier(), r.Repository, ref.Identifier(), oras.CopyOptions{
		CopyGraphOptions: oras.CopyGraphOptions{
			PreCopy: func(_ context.Context, desc ociV1.Descriptor) error {
				logger.Info("uploading", "digest", desc.Digest.String(), "mediaType", desc.MediaType)

				return nil
			},
			PostCopy: func(_ context.Context, desc ociV1.Descriptor) error {
				logger.Info("uploading", "digest", desc.Digest.String(), "mediaType", desc.MediaType)

				return nil
			},
			OnCopySkipped: func(_ context.Context, desc ociV1.Descriptor) error {
				logger.Info("uploading", "digest", desc.Digest.String(), "mediaType", desc.MediaType)

				return nil
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("error copying snapshot: %w", err)
	}

	return desc.Digest, nil
}

// CreateRepositoryName creates a name for an OCI repository and returns a hashed string from the passed arguments. The
// purpose of this function is to sanitize any passed string to an OCI repository compliant name.
func CreateRepositoryName(args ...string) (string, error) {
	hash, err := hashstructure.Hash(args, hashstructure.FormatV2, nil)
	if err != nil {
		return "", fmt.Errorf("failed to hash identity: %w", err)
	}

	repositoryName := fmt.Sprintf("sha-%d", hash)

	match, err := regexp.MatchString(v1alpha1.OCIRepositoryNameConstraints, repositoryName)
	if err != nil {
		return "", fmt.Errorf("failed to check OCI repository repositoryName constraints: %w", err)
	}

	if !match {
		return "", fmt.Errorf("repositoryName '%s' failed to match OCI repository repositoryName constraints: %w", repositoryName, err)
	}

	return repositoryName, nil
}

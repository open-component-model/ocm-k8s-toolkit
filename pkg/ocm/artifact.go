package ocm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openfluxcd/controller-manager/storage"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"ocm.software/ocm/api/ocm/compdesc"

	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// GetComponentSetForArtifact returns the component descriptor set for the given artifact.
func GetComponentSetForArtifact(storage *storage.Storage, artifact *artifactv1.Artifact) (_ *compdesc.ComponentVersionSet, retErr error) {
	tmp, err := os.MkdirTemp("", "component-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, os.RemoveAll(tmp))
	}()

	// Instead of using the http-functionality of the storage-server, we use the storage directly for performance reasons.
	// This assumes that the controllers and the storage are running in the same pod.
	unlock, err := storage.Lock(artifact)
	if err != nil {
		return nil, fmt.Errorf("failed to lock artifact: %w", err)
	}
	defer unlock()

	filePath := filepath.Join(tmp, v1alpha1.OCMComponentDescriptorList)

	if err := storage.CopyToPath(artifact, v1alpha1.OCMComponentDescriptorList, filePath); err != nil {
		return nil, fmt.Errorf("failed to copy artifact to path: %w", err)
	}

	// Read component descriptor list
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open component descriptor: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, file.Close())
	}()

	// Get component descriptor set
	cds := &Descriptors{}

	if err := yaml.NewYAMLToJSONDecoder(file).Decode(cds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal component descriptors: %w", err)
	}

	return compdesc.NewComponentVersionSet(cds.List...), nil
}

// GetAndVerifyArtifactForCollectable gets the artifact for the given collectable and verifies it against the given strg.
// If the artifact is not found, an error is returned.
func GetAndVerifyArtifactForCollectable(
	ctx context.Context,
	reader ctrl.Reader,
	strg *storage.Storage,
	collectable storage.Collectable,
) (*artifactv1.Artifact, error) {
	artifact := strg.NewArtifactFor(collectable.GetKind(), collectable.GetObjectMeta(), "", "")
	if err := reader.Get(ctx, types.NamespacedName{Name: artifact.Name, Namespace: artifact.Namespace}, artifact); err != nil {
		return nil, fmt.Errorf("failed to get artifact: %w", err)
	}

	// Check the digest of the archive and compare it to the one in the artifact
	if err := strg.VerifyArtifact(artifact); err != nil {
		return nil, fmt.Errorf("failed to verify artifact: %w", err)
	}

	return artifact, nil
}

// ValidateArtifactForCollectable verifies if the artifact for the given collectable is valid.
// This means that the artifact must be present in the cluster the reader is connected to and
// the artifact must be present in the storage.
// Additionally, the digest of the artifact must be different from the file name of the artifact.
//
// This method can be used to determine if an artifact needs an update or not because an artifact that does not
// fulfill these conditions can be considered out of date (not in the cluster, not in the storage, or mismatching digest).
//
// Prerequisite for this method is that the artifact name is based on its original digest.
func ValidateArtifactForCollectable(
	ctx context.Context,
	reader ctrl.Reader,
	strg *storage.Storage,
	collectable storage.Collectable,
	digest string,
) (bool, error) {
	artifact, err := GetAndVerifyArtifactForCollectable(ctx, reader, strg, collectable)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if ctrl.IgnoreNotFound(err) != nil {
		return false, fmt.Errorf("failed to get artifact: %w", err)
	}
	if artifact == nil {
		return false, nil
	}

	existingFile := filepath.Base(strg.LocalPath(artifact))

	return existingFile != digest, nil
}

// RemoveArtifactForCollectable removes the artifact for the given collectable from the given storage.
func RemoveArtifactForCollectable(
	ctx context.Context,
	client ctrl.Client,
	strg *storage.Storage,
	collectable storage.Collectable,
) error {
	artifact, err := GetAndVerifyArtifactForCollectable(ctx, client, strg, collectable)
	if ctrl.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get artifact: %w", err)
	}

	if artifact != nil {
		if err := strg.Remove(artifact); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("failed to remove artifact: %w", err)
			}
		}
	}

	return nil
}

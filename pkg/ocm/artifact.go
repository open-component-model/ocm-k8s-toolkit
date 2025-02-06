package ocm

import (
	"context"
	"errors"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"ocm.software/ocm/api/ocm/compdesc"

	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
)

// GetComponentSetForSnapshot returns the component descriptor set for the given artifact.
func GetComponentSetForSnapshot(ctx context.Context, repository snapshot.RepositoryType, snapshotResource *v1alpha1.Snapshot) (*compdesc.ComponentVersionSet, error) {
	reader, err := repository.FetchSnapshot(ctx, snapshotResource.GetDigest())
	if err != nil {
		return nil, err
	}

	// Get component descriptor set
	cds := &Descriptors{}
	if err := yaml.NewYAMLToJSONDecoder(reader).Decode(cds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal component descriptors: %w", err)
	}

	return compdesc.NewComponentVersionSet(cds.List...), nil
}

// GetAndVerifySnapshotForOwner gets the artifact for the given collectable and verifies it against the given strg.
// If the artifact is not found, an error is returned.
func GetAndVerifySnapshotForOwner(
	ctx context.Context,
	reader ctrl.Reader,
	registry snapshot.RegistryType,
	owner v1alpha1.SnapshotWriter,
) (*v1alpha1.Snapshot, error) {
	snapshotRef := owner.GetSnapshotName()
	if snapshotRef == "" {
		return nil, os.ErrNotExist
	}

	snapshotCR := &v1alpha1.Snapshot{}
	if err := reader.Get(ctx, types.NamespacedName{Name: snapshotRef, Namespace: owner.GetNamespace()}, snapshotCR); err != nil {
		return nil, fmt.Errorf("failed to get snapshot %s: %w", snapshotRef, err)
	}

	repository, err := registry.NewRepository(ctx, snapshotCR.Spec.Repository)
	if err != nil {
		return nil, fmt.Errorf("failed to createry: %w", err)
	}

	exists, err := repository.ExistsSnapshot(ctx, snapshotCR.GetDigest())
	if err != nil {
		return nil, fmt.Errorf("failed to check snapshot existence: %w", err)
	}

	if !exists {
		return nil, fmt.Errorf("snapshot %s does not exist", snapshotRef)
	}

	// TODO: Discuss if we need more verification steps (which are even possible?)
	// We could check if snapshotCR.Blob.Digest == layer.Digest()
	// Problem how to make sure that snapshotCR.Blob.Digest & layer.Digest are calculated the same way?

	return snapshotCR, nil
}

// ValidateSnapshotForOwner verifies if the artifact for the given collectable is valid.
// This means that the artifact must be present in the cluster the reader is connected to and
// the artifact must be present in the storage.
// Additionally, the digest of the artifact must be different from the file name of the artifact.
//
// This method can be used to determine if an artifact needs an update or not because an artifact that does not
// fulfill these conditions can be considered out of date (not in the cluster, not in the storage, or mismatching digest).
func ValidateSnapshotForOwner(
	ctx context.Context,
	reader ctrl.Reader,
	registry snapshot.RegistryType,
	owner v1alpha1.SnapshotWriter,
	digest string,
) (bool, error) {
	snapshotCR, err := GetAndVerifySnapshotForOwner(ctx, reader, registry, owner)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if ctrl.IgnoreNotFound(err) != nil {
		return false, fmt.Errorf("failed to get snapshot: %w", err)
	}
	if err != nil {
		return false, fmt.Errorf("failed to get and verify snapshot: %w", err)
	}

	if snapshotCR == nil {
		return false, nil
	}

	return snapshotCR.Spec.Blob.Digest != digest, nil
}

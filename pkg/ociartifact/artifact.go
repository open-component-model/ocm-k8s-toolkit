package ociartifact

import (
	"context"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// DeleteForObject checks if the object holds a name for an OCI repository, checks if the OCI repository exists, and if
// so, deletes the OCI artifact from the OCI repository.
func DeleteForObject(ctx context.Context, registry RegistryType, obj v1alpha1.OCIArtifactCreator) error {
	info := obj.GetOCIArtifact()
	if info == nil {
		return nil
	}

	if info.Repository != "" {
		ociRepository, err := registry.NewRepository(ctx, info.Repository)
		if err != nil {
			return err
		}

		exists, err := ociRepository.ExistsArtifact(ctx, info.Digest)
		if err != nil {
			return err
		}

		if exists {
			return ociRepository.DeleteArtifact(ctx, info.Digest)
		}
	}

	return nil
}

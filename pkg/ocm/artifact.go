package ocm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	"k8s.io/apimachinery/pkg/util/yaml"
	"ocm.software/ocm/api/ocm/compdesc"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/rerror"
)

// GetComponentSetForArtifact returns the component descriptor set for the given artifact.
func GetComponentSetForArtifact(ctx context.Context, storage *storage.Storage, artifact *artifactv1.Artifact) (_ *compdesc.ComponentVersionSet, retErr rerror.ReconcileError) {
	log.FromContext(ctx).V(1).Info("getting component set")

	tmp, err := os.MkdirTemp("", "component-*")
	if err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to create temporary directory: %w", err))
	}
	defer func() {
		retErr = rerror.AsRetryableError(errors.Join(retErr, os.RemoveAll(tmp)))
	}()

	// Instead of using the http-functionality of the storage-server, we use the storage directly for performance reasons.
	// This assumes that the controllers and the storage are running in the same pod.
	unlock, err := storage.Lock(*artifact)
	if err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to lock artifact: %w", err))
	}
	defer unlock()

	filePath := filepath.Join(tmp, v1alpha1.OCMComponentDescriptorList)

	if err := storage.CopyToPath(artifact, v1alpha1.OCMComponentDescriptorList, filePath); err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to copy artifact to path: %w", err))
	}

	// Read component descriptor list
	file, err := os.Open(filepath.Join(tmp, v1alpha1.OCMComponentDescriptorList))
	if err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to open component descriptor: %w", err))
	}
	defer func() {
		retErr = rerror.AsRetryableError(errors.Join(retErr, file.Close()))
	}()

	// Get component descriptor set
	cds := &Descriptors{}
	const bufferSize = 4096
	decoder := yaml.NewYAMLOrJSONDecoder(file, bufferSize)
	if err := decoder.Decode(cds); err != nil {
		return nil, rerror.AsRetryableError(fmt.Errorf("failed to unmarshal component descriptors: %w", err))
	}

	return compdesc.NewComponentVersionSet(cds.List...), retErr
}

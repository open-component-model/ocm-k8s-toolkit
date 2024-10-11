package ocm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	"k8s.io/apimachinery/pkg/util/yaml"
	"ocm.software/ocm/api/ocm/compdesc"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

func ComponentSetFromLocalArtifact(storage *storage.Storage, artifact *artifactv1.Artifact) (_ *compdesc.ComponentVersionSet, retErr error) {
	tmp, err := os.MkdirTemp("", "component-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, os.RemoveAll(tmp))
	}()

	unlock, err := storage.Lock(*artifact)
	if err != nil {
		return nil, fmt.Errorf("failed to lock artifact: %w", err)
	}
	defer unlock()

	filePath := filepath.Join(tmp, artifact.Name)

	if err := storage.CopyToPath(artifact, "", filePath); err != nil {
		return nil, fmt.Errorf("failed to copy artifact to path: %w", err)
	}

	// Read component descriptor list
	file, err := os.Open(filepath.Join(filePath, v1alpha1.OCMComponentDescriptorList))
	if err != nil {
		return nil, fmt.Errorf("failed to open component descriptor: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, file.Close())
	}()

	// Get component descriptor set
	cds := &Descriptors{}
	const bufferSize = 4096
	decoder := yaml.NewYAMLOrJSONDecoder(file, bufferSize)
	if err := decoder.Decode(cds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal component descriptors: %w", err)
	}

	return compdesc.NewComponentVersionSet(cds.List...), nil
}

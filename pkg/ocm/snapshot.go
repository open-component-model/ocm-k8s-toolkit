package ocm

import (
	"bytes"
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/util/yaml"
	"ocm.software/ocm/api/ocm/compdesc"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
)

// GetComponentSetForSnapshot returns the component descriptor set for the given snapshot.
func GetComponentSetForSnapshot(ctx context.Context, repository snapshot.RepositoryType, snapshotResource *v1alpha1.Snapshot) (*compdesc.ComponentVersionSet, error) {
	data, err := repository.FetchSnapshot(ctx, snapshotResource.GetDigest())
	if err != nil {
		return nil, err
	}

	cds := &Descriptors{}
	if err := yaml.NewYAMLToJSONDecoder(bytes.NewReader(data)).Decode(cds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal component descriptors: %w", err)
	}

	return compdesc.NewComponentVersionSet(cds.List...), nil
}

package types

import (
	"io"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

type LocalizationSource interface {
	Open() (io.ReadCloser, error)
	UnpackIntoDirectory(path string) (err error)

	// GetDigest is the digest of the packed target in the form of '<algorithm>:<checksum>'.
	GetDigest() string
	// GetRevision is a human-readable identifier traceable in the origin source system.
	// It can be a Git commit SHA, Git tag, a Helm chart version, etc.
	GetRevision() string
}

type LocalizationSourceWithStrategy interface {
	LocalizationSource
	GetStrategy() v1alpha1.LocalizationStrategy
}

func NewLocalizationSourceWithStrategy(source LocalizationSource, strategy v1alpha1.LocalizationStrategy) LocalizationSourceWithStrategy {
	return &localizationSourceWithStrategyImpl{
		LocalizationSource: source,
		Strategy:           strategy,
	}
}

type localizationSourceWithStrategyImpl struct {
	LocalizationSource
	Strategy v1alpha1.LocalizationStrategy
}

var _ LocalizationSource = &localizationSourceWithStrategyImpl{}

func (c *localizationSourceWithStrategyImpl) GetStrategy() v1alpha1.LocalizationStrategy {
	return c.Strategy
}

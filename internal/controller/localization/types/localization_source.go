package types

import (
	"io"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// LocalizationSource is a source of localization.
// It contains instructions on how to localize a resource.
type LocalizationSource interface {
	// Open returns a reader for instruction. It can be a tarball, a file, etc.
	// The caller is responsible for closing the reader.
	Open() (io.ReadCloser, error)
	// UnpackIntoDirectory unpacks the data into the given directory.
	// It returns an error if the directory already exists.
	// It returns an error if the source cannot be unpacked.
	UnpackIntoDirectory(path string) (err error)

	// GetDigest is the digest of the packed source in the form of '<algorithm>:<checksum>'.
	// It can be used to verify the integrity of the target or to identify it.
	GetDigest() string

	// GetRevision is a human-readable identifier traceable in the origin source system.
	// It can be a Git commit SHA, Git tag, a Helm chart version, etc.
	// It could be used to identify the source but ideally GetDigest should be used for this purpose
	GetRevision() string
}

// LocalizationSourceWithStrategy is a source of localization with a strategy on how to interpret the LocalizationSource.
// Thus it contains instructions on how to localize a resource and how to interpret them with a strategy.
type LocalizationSourceWithStrategy interface {
	// LocalizationSource is the original source of localization.
	LocalizationSource
	// GetStrategy returns the strategy on how to interpret the LocalizationSource.
	GetStrategy() v1alpha1.LocalizationStrategy
}

// NewLocalizationSourceWithStrategy creates a new LocalizationSourceWithStrategy by combining a LocalizationSource and a v1alpha1.LocalizationStrategy.
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

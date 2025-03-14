package types

import (
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
)

// LocalizationReference can be used both as a source (LocalizationConfig),
// and as a target (LocalizationTarget) for localization.
type LocalizationReference interface {
	LocalizationConfig
	LocalizationTarget
}

// LocalizationConfig is a configuration on how to localize an ociartifact.Content.
type LocalizationConfig interface {
	ociartifact.Content
}

// LocalizationTarget is a target ociartifact.Content for localization.
type LocalizationTarget interface {
	ociartifact.Content
}

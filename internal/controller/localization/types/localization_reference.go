package types

import "github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"

// LocalizationReference can be used both as a source (LocalizationConfig),
// and as a target (LocalizationTarget) for localization.
type LocalizationReference interface {
	LocalizationConfig
	LocalizationTarget
}

// LocalizationConfig is a configuration on how to localize an artifact.Content.
type LocalizationConfig interface {
	artifact.Content
}

// LocalizationTarget is a target artifact.Content for localization.
type LocalizationTarget interface {
	artifact.Content
}

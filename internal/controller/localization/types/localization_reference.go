package types

import "github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"

// LocalizationReference is a reference to a util that can be localized.
// It can be used both as a source (LocalizationConfig) and as a target (LocalizationTarget) for localization.
type LocalizationReference interface {
	LocalizationConfig
	LocalizationTarget
}

// LocalizationConfig is a source of localization.
// It contains instructions on how to localize a util.
type LocalizationConfig interface {
	artifact.Content
}

// LocalizationTarget is a target for localization.
// It contains the data that will be localized.
type LocalizationTarget interface {
	artifact.Content
}

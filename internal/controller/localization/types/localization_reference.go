package types

import (
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
)

// LocalizationReference can be used both as a source (LocalizationConfig),
// and as a target (LocalizationTarget) for localization.
type LocalizationReference interface {
	LocalizationConfig
	LocalizationTarget
}

// LocalizationConfig is a configuration on how to localize an snapshot.Content.
type LocalizationConfig interface {
	snapshot.Content
}

// LocalizationTarget is a target snapshot.Content for localization.
type LocalizationTarget interface {
	snapshot.Content
}

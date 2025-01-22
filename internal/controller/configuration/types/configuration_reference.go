package types

import (
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
)

// ConfigurationReference can be used both as a source (ConfigurationSource),
// and as a target (ConfigurationTarget) for configuration.
type ConfigurationReference interface {
	ConfigurationSource
	ConfigurationTarget
}

// ConfigurationSource is a source of localization.
// It contains instructions on how to localize an snapshot.Content.
type ConfigurationSource interface {
	snapshot.Content
}

// ConfigurationTarget is a target for configuration.
type ConfigurationTarget interface {
	snapshot.Content
}

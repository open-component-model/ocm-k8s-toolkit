package types

import "github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"

type ConfigurationReference interface {
	ConfigurationSource
	ConfigurationTarget
}

type ConfigurationSource interface {
	artifact.Content
}

type ConfigurationTarget interface {
	artifact.Content
}

package types

import (
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"
)

type ConfigurationTarget interface {
	artifact.Content
}

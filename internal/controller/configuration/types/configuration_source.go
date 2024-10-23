package types

import (
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"
)

type ConfigurationSource interface {
	artifact.Content
}

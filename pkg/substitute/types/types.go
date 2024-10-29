package types

import "github.com/open-component-model/ocm-k8s-toolkit/pkg/substitute/steps"

type Engine interface {
	Substitute() error
	AddSteps(steps ...steps.Step)
}

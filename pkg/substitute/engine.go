package substitute

import (
	"fmt"
	"os"

	"github.com/open-component-model/ocm-k8s-toolkit/pkg/substitute/steps"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/substitute/types"
)

func NewEngine(targetPath string) (types.Engine, error) {
	fi, err := os.Stat(targetPath)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("target path %s is not a valid target directory for the substitution", targetPath)
	}

	return &engine{
		steps:    make([]steps.Step, 0),
		basePath: targetPath,
	}, nil
}

type engine struct {
	steps    []steps.Step
	basePath string
}

func (e *engine) Substitute() error {
	for _, step := range e.steps {
		err := step.Substitute(e.basePath)
		if err != nil {
			return err
		}
	}

	return nil
}

func (e *engine) AddSteps(rules ...steps.Step) {
	e.steps = append(e.steps, rules...)
}

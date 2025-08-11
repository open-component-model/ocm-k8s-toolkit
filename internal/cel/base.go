package cel

import (
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"

	ocmfunctions "github.com/open-component-model/ocm-k8s-toolkit/internal/cel/functions"
)

var BaseEnv = sync.OnceValues[*cel.Env, error](func() (*cel.Env, error) {
	return cel.NewEnv(
		ext.Lists(),
		ext.Sets(),
		ext.Strings(),
		ext.Math(),
		ext.Encoders(),
		ext.Bindings(),
		cel.OptionalTypes(),
		ocmfunctions.ToOCI(),
	)
})

package functions_test

import (
	"testing"

	"github.com/google/cel-go/common/types"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/cel/functions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/cel/lazy"
)

func TestBindingToOCI_StringReference(t *testing.T) {
	tests := []struct {
		input   string
		expects map[string]string
		err     assert.ErrorAssertionFunc
	}{
		{
			input: "registry.io/myrepo/myapp:v1",
			expects: map[string]string{
				"host":       "registry.io",
				"registry":   "registry.io",
				"repository": "myrepo/myapp",
				"tag":        "v1",
				"digest":     "",
				"reference":  "v1",
			},
		},
		{
			input: "registry.io/myrepo/myapp@sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			expects: map[string]string{
				"host":       "registry.io",
				"registry":   "registry.io",
				"repository": "myrepo/myapp",
				"tag":        "",
				"digest":     "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
				"reference":  "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			},
		},
		{
			input: "registry.io/myrepo/myapp:v1@sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			expects: map[string]string{
				"host":       "registry.io",
				"registry":   "registry.io",
				"repository": "myrepo/myapp",
				"tag":        "v1",
				"digest":     "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
				"reference":  "v1@sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			r := require.New(t)
			val := functions.BindingToOCI(types.String(tc.input))
			r.NotNil(val)
			if tc.err != nil {
				r.IsType(&types.Err{}, val)
				tc.err(t, val.(*types.Err))
			}

			r.IsType(&lazy.MapValue{}, val)
			mv := val.(*lazy.MapValue)
			a := assert.New(t)
			for k, v := range tc.expects {
				a.EqualValues(v, mv.Get(types.String(k)).Value())
			}
		})
	}
}

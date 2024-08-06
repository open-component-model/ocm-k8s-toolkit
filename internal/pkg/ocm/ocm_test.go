/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ocm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	fakeocm "github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/fakes"
)

func TestClient_GetComponentVersion(t *testing.T) {
	component := "github.com/open-component-model/ocm-demo-index"
	octx := fakeocm.NewFakeOCMContext()
	comp := &fakeocm.Component{
		Name:    component,
		Version: "v0.0.1",
	}

	require.NoError(t, octx.AddComponent(comp))

	fakeKubeClient := env.FakeKubeClient()
	ocmClient := NewClient(fakeKubeClient)

	cv := &v1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-name",
			Namespace: "default",
		},
		Spec: v1alpha1.ComponentSpec{
			Component: component,
			Semver:    "v0.0.1",
		},
	}

	repoConfig := []byte(`
    baseUrl: ghcr.io/open-component-model
    type: OCIRegistry
`)

	cva, err := ocmClient.GetComponentVersion(context.Background(), octx, cv, "v0.0.1", repoConfig)
	assert.NoError(t, err)
	assert.Equal(t, cv.Spec.Component, cva.GetName())
}

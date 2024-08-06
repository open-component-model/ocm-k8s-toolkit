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

package controller

import (
	"context"
	"os"
	"testing"

	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/ocm"
	ocmfakes "github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/ocm/fakes"
)

func TestComponentReconciler_Reconcile(t *testing.T) {
	type fields struct {
		Client        func(scheme *runtime.Scheme) client.Client
		Storage       func(c client.Client, scheme *runtime.Scheme, tmp string) *storage.Storage
		OCMClient     func(client client.Client) ocm.Contract
		ocmMockSetup  func(client ocm.Contract)
		assertError   func(t *testing.T, got error)
		assertOutcome func(t *testing.T, client client.Client)
	}
	type args struct {
		ctx context.Context
		req controllerruntime.Request
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "normal reconciliation",
			fields: fields{
				Client: func(scheme *runtime.Scheme) client.Client {
					cv := DefaultComponent.DeepCopy()
					cv.Namespace = "default"
					cv.Name = "test-component"

					repo := &v1alpha1.OCMRepository{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-repository",
							Namespace: cv.Namespace,
						},
						Spec: v1alpha1.OCMRepositorySpec{
							RepositorySpec: &apiextensionsv1.JSON{
								Raw: []byte(`{"baseUrl": "ghcr.io/open-component-model", "type": "OCIRegistry"}`),
							},
						},
					}

					return env.FakeKubeClient(WithObjects(cv, repo))
				},
				Storage: func(c client.Client, scheme *runtime.Scheme, tmp string) *storage.Storage {
					s, _ := storage.NewStorage(c, scheme, tmp, "hostname", 0, 0)

					return s
				},
				OCMClient: func(client client.Client) ocm.Contract {
					c := &ocmfakes.MockOcmClient{}
					c.GetComponentVersionReturnsForName("github.com/open-component-model/test-component", getMockComponent("github.com/open-component-model/test-component", "v0.1.0"), nil)

					return c
				},
				ocmMockSetup: nil,
				assertError: func(t *testing.T, got error) {
					require.NoError(t, got)
				},
				assertOutcome: func(t *testing.T, client client.Client) {
					cv := &v1alpha1.Component{}
					require.NoError(t, client.Get(context.Background(), types.NamespacedName{Name: "test-component", Namespace: "default"}, cv))
					assert.Equal(t, "component-default-test-component", cv.Status.ArtifactRef.Name)

					artifact := &artifactv1.Artifact{}
					require.NoError(t, client.Get(context.Background(), types.NamespacedName{Name: cv.Status.ArtifactRef.Name, Namespace: "default"}, artifact))
					assert.Equal(t, "sha256:86241027b9788e59e3fdb8a096e4e18c1854beef184b58c2f1133c62ce7e386b", artifact.Spec.Digest)
				},
			},
			args: args{
				ctx: context.Background(),
				req: controllerruntime.Request{
					NamespacedName: types.NamespacedName{
						Namespace: "default",
						Name:      "test-component",
					},
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, artifactv1.AddToScheme(scheme))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp, err := os.MkdirTemp("", "test"+tt.name)
			require.NoError(t, err)

			c := tt.fields.Client(scheme)
			r := &ComponentReconciler{
				Client:    c,
				Scheme:    scheme,
				Storage:   tt.fields.Storage(c, scheme, tmp),
				OCMClient: tt.fields.OCMClient(c),
			}
			_, err = r.Reconcile(tt.args.ctx, tt.args.req)
			tt.fields.assertError(t, err)
			tt.fields.assertOutcome(t, c)
		})
	}
}

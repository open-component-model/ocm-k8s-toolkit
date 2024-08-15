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
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/tar"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	ocmfakes "github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/ocm/fakes"
)

func TestComponentReconciler_Reconcile(t *testing.T) {
	type fields struct {
		Client        func(scheme *runtime.Scheme) client.Client
		Storage       func(c client.Client, scheme *runtime.Scheme, tmp string) *storage.Storage
		OCMClient     func(client client.Client) *ocmfakes.MockOcmClient
		assertError   func(t *testing.T, got error)
		assertOutcome func(t *testing.T, client client.Client, ocmClient *ocmfakes.MockOcmClient)
		assertStore   func(t *testing.T, store *storage.Storage, client client.Client)
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
					conditions.MarkTrue(repo, meta.ReadyCondition, meta.SucceededReason, "test")

					return env.FakeKubeClient(WithObjects(cv, repo))
				},
				Storage: func(c client.Client, scheme *runtime.Scheme, tmp string) *storage.Storage {
					s, _ := storage.NewStorage(c, scheme, tmp, "hostname", 0, 0)

					return s
				},
				OCMClient: func(client client.Client) *ocmfakes.MockOcmClient {
					c := &ocmfakes.MockOcmClient{}
					c.GetComponentVersionReturnsForName("github.com/open-component-model/test-component", getMockComponent("github.com/open-component-model/test-component", "v0.1.0"), nil)
					c.GetLatestComponentVersionReturns("v1.0.0", nil)

					return c
				},
				assertError: func(t *testing.T, got error) {
					require.NoError(t, got)
				},
				assertOutcome: func(t *testing.T, client client.Client, ocmClient *ocmfakes.MockOcmClient) {
					cv := &v1alpha1.Component{}
					require.NoError(t, client.Get(context.Background(), types.NamespacedName{Name: "test-component", Namespace: "default"}, cv))
					assert.Equal(t, "component-default-test-component", cv.Status.ArtifactRef.Name)

					artifact := &artifactv1.Artifact{}
					require.NoError(t, client.Get(context.Background(), types.NamespacedName{Name: cv.Status.ArtifactRef.Name, Namespace: "default"}, artifact))
					assert.Equal(t, "sha256:c775ceef9ac1cad66fe992725684385dcdc44f85a8cef35ec1a3fe8577360993", artifact.Spec.Digest)
				},
				assertStore: func(t *testing.T, store *storage.Storage, client client.Client) {
					//TODO: Extract this into a function like compareArchiveContentWithExpected()
					artifact := &artifactv1.Artifact{}
					require.NoError(t, client.Get(context.Background(), types.NamespacedName{Name: "component-default-test-component", Namespace: "default"}, artifact))

					url := strings.TrimPrefix(artifact.Spec.URL, "http://"+store.Hostname)
					path := filepath.Join(store.BasePath, url)
					content, err := os.ReadFile(path)
					require.NoError(t, err)
					require.NoError(t, tar.Untar(bytes.NewReader(content), store.BasePath))

					archiveContent, err := os.ReadFile(filepath.Join(store.BasePath, "component-descriptor.yaml"))
					require.NoError(t, err)

					goldenContent, err := os.ReadFile(filepath.Join("testdata", "normal_reconciliation_golden.yaml"))
					require.NoError(t, err)
					assert.Equal(t, goldenContent, archiveContent)
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
		{
			name: "normal reconciliation with component references",
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
					conditions.MarkTrue(repo, meta.ReadyCondition, meta.SucceededReason, "test")

					return env.FakeKubeClient(WithObjects(cv, repo))
				},
				Storage: func(c client.Client, scheme *runtime.Scheme, tmp string) *storage.Storage {
					s, _ := storage.NewStorage(c, scheme, tmp, "hostname", 0, 0)

					return s
				},
				OCMClient: func(client client.Client) *ocmfakes.MockOcmClient {
					c := &ocmfakes.MockOcmClient{}
					c.GetComponentVersionReturnsForName(
						"github.com/open-component-model/test-component",
						getMockComponentWithReference(
							"github.com/open-component-model/test-component",
							"v0.1.0",
							"github.com/open-component-model/embedded",
							"v0.2.0",
						),
						nil,
					)
					c.GetComponentVersionReturnsForName(
						"github.com/open-component-model/embedded",
						getMockComponent(
							"github.com/open-component-model/embedded",
							"v0.2.0",
						),
						nil,
					)
					c.GetLatestComponentVersionReturns("v0.1.0", nil)

					return c
				},
				assertError: func(t *testing.T, got error) {
					require.NoError(t, got)
				},
				assertOutcome: func(t *testing.T, client client.Client, ocmClient *ocmfakes.MockOcmClient) {
					cv := &v1alpha1.Component{}
					require.NoError(t, client.Get(context.Background(), types.NamespacedName{Name: "test-component", Namespace: "default"}, cv))
					assert.Equal(t, "component-default-test-component", cv.Status.ArtifactRef.Name)

					artifact := &artifactv1.Artifact{}
					require.NoError(t, client.Get(context.Background(), types.NamespacedName{Name: cv.Status.ArtifactRef.Name, Namespace: "default"}, artifact))
					assert.Equal(t, "sha256:653102ef9cc7e596baa1aa2e7fdcc7be8cf82c8efa1b59b699ed803a32979bec", artifact.Spec.Digest)
				},
				assertStore: func(t *testing.T, store *storage.Storage, client client.Client) {},
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
		{
			name: "should not reconcile if version is already at latest",
			fields: fields{
				Client: func(scheme *runtime.Scheme) client.Client {
					cv := DefaultComponent.DeepCopy()
					cv.Namespace = "default"
					cv.Name = "test-component"
					cv.Status.Component = v1alpha1.ComponentInfo{
						Version:   "v0.0.2",
						Component: "github.com/open-component-model/test-component",
					}

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
					conditions.MarkTrue(repo, meta.ReadyCondition, meta.SucceededReason, "test")

					return env.FakeKubeClient(WithObjects(cv, repo))
				},
				Storage: func(c client.Client, scheme *runtime.Scheme, tmp string) *storage.Storage {
					s, _ := storage.NewStorage(c, scheme, tmp, "hostname", 0, 0)

					return s
				},
				OCMClient: func(client client.Client) *ocmfakes.MockOcmClient {
					c := &ocmfakes.MockOcmClient{}
					c.GetLatestComponentVersionReturns("v0.0.1", nil)

					return c
				},
				assertError: func(t *testing.T, got error) {
					require.NoError(t, got)
				},
				assertOutcome: func(t *testing.T, client client.Client, ocmClient *ocmfakes.MockOcmClient) {
					cv := &v1alpha1.Component{}
					require.NoError(t, client.Get(context.Background(), types.NamespacedName{Name: "test-component", Namespace: "default"}, cv))
					assert.Empty(t, cv.Status.ArtifactRef.Name)

					artifact := &artifactv1.Artifact{}
					require.Error(t, client.Get(context.Background(), types.NamespacedName{Name: cv.Status.ArtifactRef.Name, Namespace: "default"}, artifact))

					assert.True(t, ocmClient.GetComponentVersionWasNotCalled())
				},
				assertStore: func(t *testing.T, store *storage.Storage, client client.Client) {},
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
		{
			name: "greater version triggers reconcile and creates a new artifact",
			fields: fields{
				Client: func(scheme *runtime.Scheme) client.Client {
					cv := DefaultComponent.DeepCopy()
					cv.Namespace = "default"
					cv.Name = "test-component"
					cv.Status.Component = v1alpha1.ComponentInfo{
						Version:   "v0.1.0",
						Component: "github.com/open-component-model/test-component",
					}
					cv.Status.ArtifactRef = v1.LocalObjectReference{
						Name: "component-default-test-component",
					}
					artifact := &artifactv1.Artifact{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "component-default-test-component",
							Namespace: "default",
						},
						Spec: artifactv1.ArtifactSpec{
							Revision: "github.com-open-component-model-test-component-v0.1.0",
							Digest:   "sha256:bdd6f8e2801d4167464bf49d612c6f12e78fd2a5485d0ebac75c31b8ce71833f",
						},
					}

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
					conditions.MarkTrue(repo, meta.ReadyCondition, meta.SucceededReason, "test")

					return env.FakeKubeClient(WithObjects(cv, repo, artifact))
				},
				Storage: func(c client.Client, scheme *runtime.Scheme, tmp string) *storage.Storage {
					s, _ := storage.NewStorage(c, scheme, tmp, "hostname", 0, 0)

					return s
				},
				OCMClient: func(client client.Client) *ocmfakes.MockOcmClient {
					c := &ocmfakes.MockOcmClient{}
					c.GetLatestComponentVersionReturns("v0.2.0", nil)
					c.GetComponentVersionReturnsForName(
						"github.com/open-component-model/test-component",
						getMockComponent(
							"github.com/open-component-model/test-component",
							"v0.2.0",
						),
						nil,
					)

					return c
				},
				assertError: func(t *testing.T, got error) {
					require.NoError(t, got)
				},
				assertOutcome: func(t *testing.T, client client.Client, ocmClient *ocmfakes.MockOcmClient) {
					cv := &v1alpha1.Component{}
					require.NoError(t, client.Get(context.Background(), types.NamespacedName{Name: "test-component", Namespace: "default"}, cv))
					assert.Equal(t, "component-default-test-component", cv.Status.ArtifactRef.Name)

					artifact := &artifactv1.Artifact{}
					require.NoError(t, client.Get(context.Background(), types.NamespacedName{Name: cv.Status.ArtifactRef.Name, Namespace: "default"}, artifact))
					assert.Equal(t, "sha256:bdd6f8e2801d4167464bf49d612c6f12e78fd2a5485d0ebac75c31b8ce71833f", artifact.Spec.Digest)
					assert.Equal(t, "github.com-open-component-model-test-component-v0.2.0", artifact.Spec.Revision)
				},
				assertStore: func(t *testing.T, store *storage.Storage, client client.Client) {},
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
		{
			name: "should fail if verification fails",
			fields: fields{
				Client: func(scheme *runtime.Scheme) client.Client {
					cv := DefaultComponent.DeepCopy()
					cv.Namespace = "default"
					cv.Name = "test-component"
					cv.Status.Component = v1alpha1.ComponentInfo{
						Version:   "v0.1.0",
						Component: "github.com/open-component-model/test-component",
					}
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
					conditions.MarkTrue(repo, meta.ReadyCondition, meta.SucceededReason, "test")

					return env.FakeKubeClient(WithObjects(cv, repo))
				},
				Storage: func(c client.Client, scheme *runtime.Scheme, tmp string) *storage.Storage {
					s, _ := storage.NewStorage(c, scheme, tmp, "hostname", 0, 0)

					return s

				},
				OCMClient: func(client client.Client) *ocmfakes.MockOcmClient {
					c := &ocmfakes.MockOcmClient{}
					c.GetLatestComponentVersionReturns("v0.2.0", nil)
					c.GetComponentVersionReturnsForName(
						"github.com/open-component-model/test-component",
						getMockComponent(
							"github.com/open-component-model/test-component",
							"v0.2.0",
						),
						nil,
					)
					c.VerifyComponentReturns(errors.New("nope"))

					return c
				},
				assertError: func(t *testing.T, got error) {
					assert.ErrorContains(t, got, "failed to verify component: nope")
				},
				assertOutcome: func(t *testing.T, client client.Client, ocmClient *ocmfakes.MockOcmClient) {},
				assertStore:   func(t *testing.T, store *storage.Storage, client client.Client) {},
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
			recorder := &record.FakeRecorder{
				Events:        make(chan string, 32),
				IncludeObject: true,
			}
			tmp, err := os.MkdirTemp("", "")
			require.NoError(t, err)

			c := tt.fields.Client(scheme)
			ocmClient := tt.fields.OCMClient(c)

			store := tt.fields.Storage(c, scheme, tmp)
			r := &ComponentReconciler{
				Client:        c,
				Scheme:        scheme,
				Storage:       store,
				OCMClient:     ocmClient,
				EventRecorder: recorder,
			}
			_, err = r.Reconcile(tt.args.ctx, tt.args.req)
			tt.fields.assertError(t, err)
			close(recorder.Events)

			tt.fields.assertOutcome(t, c, ocmClient)
			tt.fields.assertStore(t, store, c)
		})
	}
}

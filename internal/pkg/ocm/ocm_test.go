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
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	fakeocm "github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/fakes"
)

const (
	Signature = "test-signature"
)

func TestClientGetComponentVersion(t *testing.T) {
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

	cva, err := ocmClient.GetComponentVersion(context.Background(), octx, cv.Spec.Component, "v0.0.1", repoConfig)
	assert.NoError(t, err)
	assert.Equal(t, cv.Spec.Component, cva.GetName())
}

func TestClientGetLatestValidComponentVersion(t *testing.T) {
	component := "github.com/open-component-model/ocm-demo-index"
	octx := fakeocm.NewFakeOCMContext()
	comp := &fakeocm.Component{
		Name:    component,
		Version: "v0.0.2",
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

	version, err := ocmClient.GetLatestValidComponentVersion(context.Background(), octx, cv, repoConfig)
	require.EqualError(t, err, "no matching versions found for constraint 'v0.0.1'")
	assert.Empty(t, version)
}

func TestClient_VerifyComponent(t *testing.T) {
	publicKey1, err := os.ReadFile(filepath.Join("testdata", "public1_key.pem"))
	require.NoError(t, err)
	privateKey, err := os.ReadFile(filepath.Join("testdata", "private_key.pem"))
	require.NoError(t, err)

	secretName := "sign-secret"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: "default",
		},
		Data: map[string][]byte{
			Signature: publicKey1,
		},
	}
	fakeKubeClient := env.FakeKubeClient(WithObjects(secret))
	ocmClient := NewClient(fakeKubeClient)
	component := "github.com/open-component-model/ocm-demo-index"

	octx := fakeocm.NewFakeOCMContext()

	c := &fakeocm.Component{
		Name:    component,
		Version: "v0.0.1",
		Sign: &fakeocm.Sign{
			Name:    Signature,
			PrivKey: privateKey,
			PubKey:  publicKey1,
			Digest:  "3d879ecdea45acb7f8d85b89fd653288d84af4476eac4141822142ec59c13745",
		},
	}
	require.NoError(t, octx.AddComponent(c))

	cv := &v1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-name",
			Namespace: "default",
		},
		Spec: v1alpha1.ComponentSpec{
			Component: component,
			Semver:    "v0.0.1",
			RepositoryRef: v1alpha1.ObjectKey{
				Namespace: "default",
				Name:      "test-repository",
			},
			Verify: []v1alpha1.Verification{
				{
					Signature: Signature,
					SecretRef: secretName,
				},
			},
		},
	}

	err = ocmClient.VerifyComponent(context.Background(), octx, cv, "v0.0.1", []byte(`baseUrl: https://example.com`))
	require.NoError(t, err)
}

func TestClient_VerifyComponentWithValueKey(t *testing.T) {
	publicKey1, err := os.ReadFile(filepath.Join("testdata", "public1_key.pem"))
	require.NoError(t, err)
	privateKey, err := os.ReadFile(filepath.Join("testdata", "private_key.pem"))
	require.NoError(t, err)

	fakeKubeClient := env.FakeKubeClient()
	ocmClient := NewClient(fakeKubeClient)
	component := "github.com/open-component-model/ocm-demo-index"

	octx := fakeocm.NewFakeOCMContext()

	c := &fakeocm.Component{
		Name:    component,
		Version: "v0.0.1",
		Sign: &fakeocm.Sign{
			Name:    Signature,
			PrivKey: privateKey,
			PubKey:  publicKey1,
			Digest:  "3d879ecdea45acb7f8d85b89fd653288d84af4476eac4141822142ec59c13745",
		},
	}
	require.NoError(t, octx.AddComponent(c))
	//var buffer []byte
	pubKey := base64.StdEncoding.EncodeToString(publicKey1)
	cv := &v1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-name",
			Namespace: "default",
		},
		Spec: v1alpha1.ComponentSpec{
			Component: component,
			Semver:    "v0.0.1",
			RepositoryRef: v1alpha1.ObjectKey{
				Namespace: "default",
				Name:      "test-repo",
			},
			Verify: []v1alpha1.Verification{
				{
					Signature: Signature,
					Value:     pubKey,
				},
			},
		},
	}

	err = ocmClient.VerifyComponent(context.Background(), octx, cv, "v0.0.1", []byte(`baseUrl: https://example.com`))
	require.NoError(t, err)
}

func TestClient_VerifyComponentWithValueKeyFailsIfValueIsEmpty(t *testing.T) {
	publicKey1, err := os.ReadFile(filepath.Join("testdata", "public1_key.pem"))
	require.NoError(t, err)
	privateKey, err := os.ReadFile(filepath.Join("testdata", "private_key.pem"))
	require.NoError(t, err)

	fakeKubeClient := env.FakeKubeClient()
	ocmClient := NewClient(fakeKubeClient)
	component := "github.com/open-component-model/ocm-demo-index"

	octx := fakeocm.NewFakeOCMContext()

	c := &fakeocm.Component{
		Name:    component,
		Version: "v0.0.1",
		Sign: &fakeocm.Sign{
			Name:    Signature,
			PrivKey: privateKey,
			PubKey:  publicKey1,
			Digest:  "3d879ecdea45acb7f8d85b89fd653288d84af4476eac4141822142ec59c13745",
		},
	}
	require.NoError(t, octx.AddComponent(c))
	cv := &v1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-name",
			Namespace: "default",
		},
		Spec: v1alpha1.ComponentSpec{
			Component:     component,
			Semver:        "v0.0.1",
			RepositoryRef: v1alpha1.ObjectKey{Namespace: "default", Name: "test-repository"},
			Verify: []v1alpha1.Verification{
				{
					Signature: Signature,
					Value:     "",
				},
			},
		},
	}

	err = ocmClient.VerifyComponent(context.Background(), octx, cv, "v0.0.1", []byte(`baseUrl: https://example.com`))
	assert.EqualError(t, err, "kubernetes secret reference not provided")
}

func TestClient_VerifyComponentDifferentPublicKey(t *testing.T) {
	publicKey2, err := os.ReadFile(filepath.Join("testdata", "public2_key.pem"))
	require.NoError(t, err)
	privateKey, err := os.ReadFile(filepath.Join("testdata", "private_key.pem"))
	require.NoError(t, err)

	secretName := "sign-secret"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: "default",
		},
		Data: map[string][]byte{
			Signature: publicKey2,
		},
	}
	fakeKubeClient := env.FakeKubeClient(WithObjects(secret))
	ocmClient := NewClient(fakeKubeClient)
	component := "github.com/open-component-model/ocm-demo-index"

	octx := fakeocm.NewFakeOCMContext()

	c := &fakeocm.Component{
		Name:    component,
		Version: "v0.0.1",
		Sign: &fakeocm.Sign{
			Name:    Signature,
			PrivKey: privateKey,
			PubKey:  publicKey2,
			Digest:  "3d879ecdea45acb7f8d85b89fd653288d84af4476eac4141822142ec59c13745",
		},
	}
	require.NoError(t, octx.AddComponent(c))

	cv := &v1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-name",
			Namespace: "default",
		},
		Spec: v1alpha1.ComponentSpec{
			Component:     component,
			Semver:        "v0.0.1",
			RepositoryRef: v1alpha1.ObjectKey{Namespace: "default", Name: "test-repository"},
			Verify: []v1alpha1.Verification{
				{
					Signature: Signature,
					SecretRef: secretName,
				},
			},
		},
	}

	err = ocmClient.VerifyComponent(context.Background(), octx, cv, "v0.0.1", []byte(`baseUrl: https://example.com`))
	require.Error(t, err)
}

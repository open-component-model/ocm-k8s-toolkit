package oci

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	_ "github.com/distribution/distribution/v3/registry/storage/driver/inmemory"
	"github.com/mitchellh/hashstructure/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ocmmetav1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

func TestClient_FetchPush(t *testing.T) {
	scheme := runtime.NewScheme()
	assert.NoError(t, v1alpha1.AddToScheme(scheme))
	assert.NoError(t, v1.AddToScheme(scheme))

	addr := strings.TrimPrefix(testServer.URL, "http://")
	testCases := []struct {
		name     string
		blob     []byte
		expected []byte
		resource v1alpha1.ResourceReference
		objects  []client.Object
		push     bool
	}{
		{
			name:     "image",
			blob:     []byte("image"),
			expected: []byte("image"),
			resource: v1alpha1.ResourceReference{
				Resource: ocmmetav1.Identity{
					"name":    "test-resource-1",
					"version": "v0.0.1",
				},
			},
			push: true,
		},
		{
			name:     "empty image",
			blob:     []byte(""),
			expected: []byte(""),
			resource: v1alpha1.ResourceReference{
				Resource: ocmmetav1.Identity{
					"name":    "test-resource-2",
					"version": "v0.0.2",
				},
			},
			push: true,
		},
		{
			name:     "data doesn't exist",
			blob:     []byte(""),
			expected: []byte(""),
			resource: v1alpha1.ResourceReference{
				Resource: ocmmetav1.Identity{
					"name":    "test-resource-3",
					"version": "v0.0.3",
				},
			},
		},
	}

	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ocm-registry-tls-certs",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"ca.crt":  []byte("file"),
			"tls.crt": []byte("file"),
			"tls.key": []byte("file"),
		},
		Type: "Opaque",
	}
	fakeClient := fake.NewClientBuilder().WithObjects(secret).WithScheme(scheme).Build()
	c := NewClient(addr, WithClient(fakeClient), WithCertificateSecret("ocm-registry-tls-certs"), WithNamespace("default"))

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			g := NewWithT(t)
			obj := &v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-name",
					Namespace: "default",
				},
				Spec: v1alpha1.ComponentSpec{
					Component: "github.com/open-component-model/root",
					Semver:    "v0.0.1",
				},
				Status: v1alpha1.ComponentStatus{
					Component: v1alpha1.ComponentInfo{
						Version: "v0.0.1",
					},
				},
			}
			identity := ocmmetav1.Identity{
				"component-version": obj.Status.Component.Version,
				"component-name":    obj.Spec.Component,
				"resource-name":     tc.resource.Resource["name"],
				"resource-version":  tc.resource.Resource["version"],
			}
			name, err := hashIdentity(identity)
			g.Expect(err).NotTo(HaveOccurred())
			if tc.push {
				_, _, err := c.PushData(context.Background(), io.NopCloser(bytes.NewBuffer(tc.blob)), "", name, tc.resource.Resource["version"])
				g.Expect(err).NotTo(HaveOccurred())
				blob, _, _, err := c.FetchDataByIdentity(context.Background(), name, tc.resource.Resource["version"])
				g.Expect(err).NotTo(HaveOccurred())
				content, err := io.ReadAll(blob)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(content).To(Equal(tc.expected))
			} else {
				exists, err := c.IsCached(context.Background(), name, tc.resource.Resource["version"])
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(exists).To(BeFalse())
			}
		})
	}
}

func TestClient_DeleteData(t *testing.T) {
	scheme := runtime.NewScheme()
	assert.NoError(t, v1.AddToScheme(scheme))
	assert.NoError(t, v1alpha1.AddToScheme(scheme))

	addr := strings.TrimPrefix(testServer.URL, "http://")
	testCases := []struct {
		name     string
		blob     []byte
		expected []byte
		resource v1alpha1.ResourceReference
		objects  []client.Object
		push     bool
	}{
		{
			name:     "image",
			blob:     []byte("image"),
			expected: []byte("image"),
			resource: v1alpha1.ResourceReference{
				Resource: ocmmetav1.Identity{
					"name":    "test-resource-1",
					"version": "v0.0.1",
				},
			},
			push: true,
		},
	}

	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ocm-registry-tls-certs",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"ca.crt":  []byte("file"),
			"tls.crt": []byte("file"),
			"tls.key": []byte("file"),
		},
		Type: "Opaque",
	}
	fakeClient := fake.NewClientBuilder().WithObjects(secret).WithScheme(scheme).Build()
	c := NewClient(addr, WithClient(fakeClient), WithCertificateSecret("ocm-registry-tls-certs"), WithNamespace("default"))

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			g := NewWithT(t)

			obj := &v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-name",
					Namespace: "default",
				},
				Spec: v1alpha1.ComponentSpec{
					Component: "github.com/open-component-model/root",
					Semver:    "v0.0.1",
				},
				Status: v1alpha1.ComponentStatus{
					Component: v1alpha1.ComponentInfo{
						Version: "v0.0.1",
					},
				},
			}
			identity := ocmmetav1.Identity{
				"component-version": obj.Status.Component.Version,
				"component-name":    obj.Spec.Component,
				"resource-name":     tc.resource.Resource["name"],
				"resource-version":  tc.resource.Resource["version"],
			}
			name, err := hashIdentity(identity)
			g.Expect(err).NotTo(HaveOccurred())
			_, _, err = c.PushData(context.Background(), io.NopCloser(bytes.NewBuffer(tc.blob)), "", name, tc.resource.Resource["version"])
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(c.DeleteData(context.Background(), name, tc.resource.Resource["version"])).To(Succeed())
			exists, err := c.IsCached(context.Background(), name, tc.resource.Resource["version"])
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(exists).To(BeFalse())
		})
	}
}

// TODO: Check purpose
// hashIdentity returns the string hash of an ocm identity.
func hashIdentity(id ocmmetav1.Identity) (string, error) {
	hash, err := hashstructure.Hash(id, hashstructure.FormatV2, nil)
	if err != nil {
		return "", fmt.Errorf("failed to hash identity: %w", err)
	}

	return fmt.Sprintf("sha-%d", hash), nil
}

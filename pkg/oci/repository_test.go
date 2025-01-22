package oci

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/google/go-containerregistry/pkg/v1/types"
)

func TestRepository_Blob(t *testing.T) {
	addr := strings.TrimPrefix(testServer.URL, "http://")
	testCases := []struct {
		name     string
		blob     []byte
		expected []byte
	}{
		{
			name:     "blob",
			blob:     []byte("blob"),
			expected: []byte("blob"),
		},
		{
			name:     "empty blob",
			blob:     []byte(""),
			expected: []byte(""),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			// create a repository client
			repoName := generateRandomName("testblob")
			repo, err := NewRepository(addr + "/" + repoName)
			g.Expect(err).NotTo(HaveOccurred())

			// compute a blob
			layer := computeStreamBlob(io.NopCloser(bytes.NewBuffer(tc.blob)), string(types.OCILayer))

			// push blob to the registry
			err = repo.pushBlob(layer)
			g.Expect(err).NotTo(HaveOccurred())

			// fetch the blob from the registry
			digest, err := layer.Digest()
			g.Expect(err).NotTo(HaveOccurred())
			rc, err := repo.FetchBlob(digest.String())
			g.Expect(err).NotTo(HaveOccurred())
			b, err := io.ReadAll(rc)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(b).To(Equal(tc.expected))
		})
	}
}

func TestRepository_StreamImage(t *testing.T) {
	addr := strings.TrimPrefix(testServer.URL, "http://")
	testCases := []struct {
		name     string
		blob     []byte
		expected []byte
	}{
		{
			name:     "image",
			blob:     []byte("image"),
			expected: []byte("image"),
		},
		{
			name:     "empty image",
			blob:     []byte(""),
			expected: []byte(""),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			// create a repository client
			repoName := generateRandomName("testimage")
			repo, err := NewRepository(addr + "/" + repoName)
			g.Expect(err).NotTo(HaveOccurred())

			// push image to the registry
			blob := tc.blob
			reader := io.NopCloser(bytes.NewBuffer(blob))
			manifest, err := repo.PushStreamingImage("latest", reader, string(types.OCILayer), map[string]string{
				"org.opencontainers.artifact.created": time.Now().UTC().Format(time.RFC3339),
			})
			g.Expect(err).NotTo(HaveOccurred())
			digest := manifest.Layers[0].Digest.String()
			layer, err := repo.FetchBlob(digest)
			g.Expect(err).NotTo(HaveOccurred())
			b, err := io.ReadAll(layer)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(b).To(Equal(tc.expected))
		})
	}
}

package compression_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/ulikunitz/xz"

	contcompression "github.com/containers/image/v5/pkg/compression"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"

	"github.com/open-component-model/ocm-k8s-toolkit/pkg/compression"
)

const testfile = "testfile"

var testData = []byte("test")

type MockStorage struct {
	data *bytes.Buffer
}

func (m *MockStorage) Copy(_ *artifactv1.Artifact, reader io.Reader) error {
	m.data = new(bytes.Buffer)
	_, err := io.Copy(m.data, reader)
	return err
}

func (m *MockStorage) GetData() *bytes.Buffer {
	return m.data
}

var _ compression.WriterToStorageFromArtifact = &MockStorage{}

func TestAutoCompressAndArchiveFile(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T) (string, *MockStorage)
		validate func(t *testing.T, storage *MockStorage)
	}{
		{
			name: "Uncompressed",
			setup: func(t *testing.T) (string, *MockStorage) {
				path := filepath.Join(t.TempDir(), testfile)
				assert.NoError(t, os.WriteFile(path, testData, 0o644))
				return path, &MockStorage{}
			},
			validate: validateAutoCompressedAsGzip,
		},
		{
			name: "Precompressed_Gzip",
			setup: func(t *testing.T) (string, *MockStorage) {
				path := filepath.Join(t.TempDir(), testfile)
				var buf bytes.Buffer
				compress := gzip.NewWriter(&buf)
				_, err := io.Copy(compress, bytes.NewReader(testData))
				assert.NoError(t, compress.Close())
				assert.NoError(t, err)
				assert.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
				return path, &MockStorage{}
			},
			validate: validateAutoCompressedAsGzip,
		},
		{
			name: "Precompressed_Nongzip_Xz",
			setup: func(t *testing.T) (string, *MockStorage) {
				path := filepath.Join(t.TempDir(), testfile)
				var buf bytes.Buffer
				compress, err := xz.NewWriter(&buf)
				assert.NoError(t, err)
				_, err = io.Copy(compress, bytes.NewReader(testData))
				assert.NoError(t, compress.Close())
				assert.NoError(t, err)
				assert.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
				return path, &MockStorage{}
			},
			validate: validateAutoCompressedAsGzip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, storage := tt.setup(t)
			art := &artifactv1.Artifact{}
			err := compression.AutoCompressAsGzipAndArchiveFile(context.Background(), art, storage, path)
			assert.NoError(t, err)
			tt.validate(t, storage)
		})
	}
}

func validateAutoCompressedAsGzip(t *testing.T, storage *MockStorage) {
	t.Helper()
	algo, decompress, reader, err := contcompression.DetectCompressionFormat(storage.GetData())
	assert.NoError(t, err)
	assert.Equal(t, contcompression.Gzip.Name(), algo.Name())
	decompressed, err := decompress(reader)
	assert.NoError(t, err)
	data, err := io.ReadAll(decompressed)
	assert.NoError(t, err)
	assert.NoError(t, decompressed.Close())
	assert.Equal(t, testData, data)
}

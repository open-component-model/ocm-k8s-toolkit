package compression_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/ulikunitz/xz"

	contcompression "github.com/containers/image/v5/pkg/compression"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/compression"
)

var testData = []byte("test")

type MockStorage struct {
	data *bytes.Buffer
}

func (m *MockStorage) Copy(_ *v1alpha1.Snapshot, reader io.Reader) error {
	m.data = new(bytes.Buffer)
	_, err := io.Copy(m.data, reader)
	return err
}

func (m *MockStorage) GetData() *bytes.Buffer {
	return m.data
}

var _ compression.WriterToStorageFromSnapshot = &MockStorage{}

func TestAutoCompressAndArchiveFile(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T) []byte
		validate func(t *testing.T, data []byte)
	}{
		{
			name: "Uncompressed",
			setup: func(t *testing.T) []byte {
				return testData
			},
			validate: validateAutoCompressedAsGzip,
		},
		{
			name: "Precompressed_Gzip",
			setup: func(t *testing.T) []byte {
				var buf bytes.Buffer
				compress := gzip.NewWriter(&buf)
				_, err := io.Copy(compress, bytes.NewReader(testData))
				assert.NoError(t, compress.Close())
				assert.NoError(t, err)
				return buf.Bytes()
			},
			validate: validateAutoCompressedAsGzip,
		},
		{
			name: "Precompressed_Nongzip_Xz",
			setup: func(t *testing.T) []byte {
				var buf bytes.Buffer
				compress, err := xz.NewWriter(&buf)
				assert.NoError(t, err)
				_, err = io.Copy(compress, bytes.NewReader(testData))
				assert.NoError(t, compress.Close())
				assert.NoError(t, err)
				return buf.Bytes()
			},
			validate: validateAutoCompressedAsGzip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.setup(t)
			dataCompressed, err := compression.AutoCompressAsGzip(context.Background(), data)
			assert.NoError(t, err)
			tt.validate(t, dataCompressed)
		})
	}
}

func validateAutoCompressedAsGzip(t *testing.T, input []byte) {
	t.Helper()
	algo, decompress, reader, err := contcompression.DetectCompressionFormat(bytes.NewReader(input))
	assert.NoError(t, err)
	assert.Equal(t, contcompression.Gzip.Name(), algo.Name())
	decompressed, err := decompress(reader)
	assert.NoError(t, err)
	data, err := io.ReadAll(decompressed)
	assert.NoError(t, err)
	assert.NoError(t, decompressed.Close())
	assert.Equal(t, testData, data)
}

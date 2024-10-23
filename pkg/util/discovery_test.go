package util

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDataFromTarOrPlain(t *testing.T) {
	// Happy path
	t.Run("reads data from tar archive", func(t *testing.T) {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		tw.WriteHeader(&tar.Header{
			Name: "test.yaml",
			Size: int64(len("content")),
		})
		tw.Write([]byte("content"))
		tw.Close()

		reader, err := DataFromTarOrPlain(&buf)
		assert.NoError(t, err)

		data, err := io.ReadAll(reader)
		assert.NoError(t, err)
		assert.Equal(t, "content", string(data))
	})

	// Edge case
	t.Run("returns error for non-regular file in tar", func(t *testing.T) {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		tw.WriteHeader(&tar.Header{
			Name:     "test.yaml",
			Typeflag: tar.TypeDir,
		})
		tw.Close()

		_, err := DataFromTarOrPlain(&buf)
		assert.Error(t, err)
	})
}

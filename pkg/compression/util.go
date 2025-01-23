package compression

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/containers/image/v5/pkg/compression"
	"sigs.k8s.io/controller-runtime/pkg/log"

	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
)

type WriterToStorageFromArtifact interface {
	Copy(art *artifactv1.Artifact, reader io.Reader) error
}

// AutoCompressAsGzipAndArchiveFile compresses the file if it is not already compressed and archives it in the storage.
// If the file is already compressed as gzip, it will be archived as is.
// If the file is not compressed or not in gzip format, it will be attempting to recompress to gzip and then archive.
// This is because some source controllers such as kustomize expect this compression format in their artifacts.
func AutoCompressAsGzipAndArchiveFile(ctx context.Context, art *artifactv1.Artifact, storage WriterToStorageFromArtifact, path string) (retErr error) {
	logger := log.FromContext(ctx).WithValues("artifact", art.Name, "path", path)
	file, err := os.OpenFile(path, os.O_RDONLY, 0o400)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, file.Close())
	}()

	algo, decompressor, reader, err := compression.DetectCompressionFormat(file)
	if err != nil {
		return fmt.Errorf("failed to detect compression format: %w", err)
	}

	// If the file is
	// 1. not compressed or
	// 2. compressed but not in gzip format
	// we will recompress it to gzip and archive it
	if decompressor == nil || algo.Name() != compression.Gzip.Name() {
		// If it is compressed in a different format, we first decompress it and then recompress it to gzip
		if decompressor != nil {
			decompressed, err := decompressor(reader)
			if err != nil {
				return fmt.Errorf("failed to decompress: %w", err)
			}
			defer func() {
				retErr = errors.Join(retErr, decompressed.Close())
			}()
			reader = decompressed
		}

		logger.V(1).Info("archiving file, but detected file is not compressed or not in gzip format, recompressing and archiving")
		// TODO: this loads the single file into memory which can be expensive, but orchestrating an io.Pipe here is not trivial
		var buf bytes.Buffer
		if err := compressViaBuffer(&buf, reader); err != nil {
			return err
		}
		if err := storage.Copy(art, &buf); err != nil {
			return fmt.Errorf("failed to copy: %w", err)
		}

		return nil
	}

	logger.V(1).Info("archiving already compressed file from path")
	if err := storage.Copy(art, reader); err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	return nil
}

func compressViaBuffer(buf *bytes.Buffer, reader io.Reader) (retErr error) {
	compressToBuf, err := compression.CompressStream(buf, compression.Gzip, nil)
	if err != nil {
		return fmt.Errorf("failed to compress stream: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, compressToBuf.Close())
	}()
	if _, err := io.Copy(compressToBuf, reader); err != nil {
		return fmt.Errorf("failed to copy: %w", err)
	}

	return nil
}

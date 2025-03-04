package compression

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/containers/image/v5/pkg/compression"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// AutoCompressAsGzip compresses the content if it is not already compressed and returns it.
// If the file is already compressed as gzip, it will be returned as is.
// If the file is not compressed or not in gzip format, it will be attempting to recompress to gzip and then return.
// This is because some source controllers such as kustomize expect this compression format in their artifacts.
func AutoCompressAsGzip(ctx context.Context, data []byte) (_ []byte, retErr error) {
	logger := log.FromContext(ctx)

	algo, decompressor, reader, err := compression.DetectCompressionFormat(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to detect compression format: %w", err)
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
				return nil, fmt.Errorf("failed to decompress: %w", err)
			}
			defer func() {
				retErr = errors.Join(retErr, decompressed.Close())
			}()
			reader = decompressed
		}

		logger.V(1).Info("gzip-compress data")
		var buf bytes.Buffer
		if err := compressViaBuffer(&buf, reader); err != nil {
			return nil, err
		}

		return buf.Bytes(), nil
	}

	// Return as is, if already compressed in gzip format
	return data, nil
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

func ExtractDataFromTGZ(data []byte) (_ []byte, retErr error) {
	buf := bytes.NewReader(data)

	gzipReader, err := gzip.NewReader(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer func() {
		retErr = gzipReader.Close()
	}()

	tarReader := tar.NewReader(gzipReader)

	_, err = tarReader.Next()
	if err != nil {
		return nil, fmt.Errorf("failed to read tar header: %w", err)
	}

	var extractedData bytes.Buffer
	//nolint:gosec // TODO: Decision needed
	if _, err := io.Copy(&extractedData, tarReader); err != nil {
		return nil, fmt.Errorf("failed to extract file content: %w", err)
	}

	return extractedData.Bytes(), nil
}

func CreateTGZFromPath(srcDir string) (_ []byte, retErr error) {
	var buf bytes.Buffer

	// Create a gzip writer
	gzipWriter := gzip.NewWriter(&buf)
	defer func() {
		retErr = gzipWriter.Close()
	}()

	// Create a tar writer
	tarWriter := tar.NewWriter(gzipWriter)
	defer func() {
		retErr = tarWriter.Close()
	}()

	// Walk through the source directory
	if err := filepath.Walk(srcDir, func(file string, fileInfo os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("could not read file %s: %w", file, err)
		}

		if !fileInfo.Mode().IsRegular() {
			return nil
		}

		if fileInfo.IsDir() {
			return nil
		}

		// Create tar fileHeader
		fileHeader, err := tar.FileInfoHeader(fileInfo, fileInfo.Name())
		if err != nil {
			return fmt.Errorf("could not create tar fileHeader: %w", err)
		}

		// Use relative path for fileHeader.Name to preserve folder structure
		relPath, err := filepath.Rel(srcDir, file)
		if err != nil {
			return fmt.Errorf("could not create relative path: %w", err)
		}
		fileHeader.Name = relPath

		// Write fileHeader
		err = tarWriter.WriteHeader(fileHeader)
		if err != nil {
			return fmt.Errorf("could not write tar fileHeader: %w", err)
		}

		// Open the file
		f, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("could not open file %s: %w", file, err)
		}
		defer func() {
			err = f.Close()
		}()

		// Copy file data into the tar archive
		_, err = io.Copy(tarWriter, f)
		if err != nil {
			return fmt.Errorf("could not copy file %s: %w", file, err)
		}

		return nil
	}); err != nil {
		return nil, fmt.Errorf("could not walk directory %s: %w", srcDir, err)
	}

	if err := tarWriter.Close(); err != nil {
		return nil, fmt.Errorf("could not close tar writer: %w", err)
	}

	if err := gzipWriter.Close(); err != nil {
		return nil, fmt.Errorf("could not close gzip writer: %w", err)
	}

	return buf.Bytes(), nil
}

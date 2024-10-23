package util

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// IsHelmChart is a hacky helper script because if the target is a Helm TGZ archive, it is stored in a subdirectory
// instead of at Root. Because we do not want users to need to know this abstracted subdirectory
// we have this function to tell us if we are dealing with one to correctly traverse into the directory
// before applying any localizations. This is safe because we only have one subdirectory in the Helm TGZ archive.
func IsHelmChart(targetDir string) (bool, string, error) {
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return false, "", fmt.Errorf("failed to read target directory: %w", err)
	}

	// if we have only one entry and it is a directory, we assume it is a Helm Chart (for now), otherwise abort
	if len(entries) != 1 || !entries[0].IsDir() {
		return false, "", nil
	}

	// now make sure we really deal with a helm chart by checking if the subdirectory contains a Chart.yaml in dir.
	subDir := filepath.Join(targetDir, entries[0].Name())
	dir, err := os.ReadDir(subDir)
	if err != nil {
		return false, "", fmt.Errorf("failed to read only target sub-directory to determine if we are dealing with a Helm Chart: %w", err)
	}
	for _, entry := range dir {
		if entry.Name() == "Chart.yaml" {
			return true, subDir, nil
		}
	}

	return false, "", nil
}

// DataFromTarOrPlain checks if the reader is a tar archive and returns a new reader that reads from the beginning of the input.
// If the input is not a tar archive, the input reader is returned as is.
func DataFromTarOrPlain(reader io.Reader) (io.Reader, error) {
	isTar, reader := IsTar(reader)
	if !isTar {
		return reader, nil
	}

	t := tar.NewReader(reader)
	fi, err := t.Next()
	if err != nil {
		return nil, fmt.Errorf("failed to read tar header even though data was recognized as tar: %w", err)
	}
	if fi.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("expected exactly one regular file in tar, got type flag %x", fi.Typeflag)
	}

	return t, nil
}

const TarPaddedHeaderSize = 512

// IsTar checks if the reader is a tar archive and returns a new reader that reads from the beginning of the input.
// The input reader should no longer be used after calling this function.
func IsTar(reader io.Reader) (bool, io.Reader) {
	var buf bytes.Buffer
	// We need to read the first 512 bytes to determine if the input is a tar archive.
	buf.Grow(TarPaddedHeaderSize)
	tr := tar.NewReader(io.TeeReader(reader, &buf))
	_, err := tr.Next()

	return err == nil, io.MultiReader(&buf, reader)
}

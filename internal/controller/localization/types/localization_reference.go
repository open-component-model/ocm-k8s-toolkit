package types

import (
	"errors"
	"io"
	"os"

	"github.com/fluxcd/pkg/tar"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
)

const modeReadOnlyUser = 0o400

var ErrAlreadyUnpacked = errors.New("already unpacked")

type ComponentLocalizationReference struct {
	LocalArtifactPath string
	Artifact          *artifactv1.Artifact
}

func (c *ComponentLocalizationReference) GetDigest() string {
	return c.Artifact.Spec.Digest
}

func (c *ComponentLocalizationReference) GetRevision() string {
	return c.Artifact.Spec.Revision
}

var _ LocalizationTarget = &ComponentLocalizationReference{}

func (c *ComponentLocalizationReference) Open() (io.ReadCloser, error) {
	return os.OpenFile(c.LocalArtifactPath, os.O_RDONLY, modeReadOnlyUser)
}

func (c *ComponentLocalizationReference) UnpackIntoDirectory(path string) (err error) {
	fi, err := os.Stat(path)
	if err == nil && fi.IsDir() {
		return ErrAlreadyUnpacked
	}

	if err = os.MkdirAll(path, os.ModeDir); err != nil {
		return err
	}

	data, err := c.Open()
	defer func() {
		err = errors.Join(err, data.Close())
	}()

	return tar.Untar(data, path)
}

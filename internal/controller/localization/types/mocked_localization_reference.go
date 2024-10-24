package types

import (
	"bytes"
	"errors"
	"io"
	"os"

	fluxtar "github.com/fluxcd/pkg/tar"
	"github.com/opencontainers/go-digest"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"
)

type MockedLocalizationReference struct {
	Path     string
	Data     []byte
	Resource *v1alpha1.Resource
}

var (
	_ LocalizationConfig = &MockedLocalizationReference{}
	_ LocalizationTarget = &MockedLocalizationReference{}
)

func (r *MockedLocalizationReference) Open() (io.ReadCloser, error) {
	if r.Data != nil {
		return io.NopCloser(bytes.NewReader(r.Data)), nil
	}

	return os.Open(r.Path)
}

func (r *MockedLocalizationReference) GetDigest() (string, error) {
	if r.Data != nil {
		return digest.NewDigestFromBytes(digest.SHA256, r.Data).String(), nil
	}

	return digest.NewDigestFromBytes(digest.SHA256, []byte(r.Path)).String(), nil
}

func (r *MockedLocalizationReference) GetRevision() string {
	return r.Path
}

func (r *MockedLocalizationReference) UnpackIntoDirectory(path string) (err error) {
	fi, err := os.Stat(path)
	if err == nil && fi.IsDir() {
		return artifact.ErrAlreadyUnpacked
	}

	if err = os.MkdirAll(path, os.ModeDir|os.ModePerm); err != nil {
		return err
	}

	data, err := r.Open()
	defer func() {
		err = errors.Join(err, data.Close())
	}()

	return fluxtar.Untar(data, path)
}

func (r *MockedLocalizationReference) GetResource() *v1alpha1.Resource {
	return r.Resource
}

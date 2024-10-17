package types

import (
	"errors"
	"io"
	"os"

	fluxtar "github.com/fluxcd/pkg/tar"
	"github.com/opencontainers/go-digest"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

const modeReadWriteUser = 0o600

var ErrAlreadyUnpacked = errors.New("already unpacked")

type LocalStorageResourceLocalizationReference struct {
	Storage  *storage.Storage
	Artifact *artifactv1.Artifact
	Resource *v1alpha1.Resource
}

type LocalizationReference interface {
	LocalizationSource
	LocalizationTarget
}

var (
	_ LocalizationSource = &LocalStorageResourceLocalizationReference{}
	_ LocalizationTarget = &LocalStorageResourceLocalizationReference{}
)

func (r *LocalStorageResourceLocalizationReference) GetDigest() string {
	return r.Artifact.Spec.Digest
}

func (r *LocalStorageResourceLocalizationReference) GetRevision() string {
	return r.Artifact.Spec.Revision
}

var _ LocalizationTarget = &LocalStorageResourceLocalizationReference{}

func (r *LocalStorageResourceLocalizationReference) Open() (io.ReadCloser, error) {
	return r.open()
}

func (r *LocalStorageResourceLocalizationReference) open() (io.ReadCloser, error) {
	path := r.Storage.LocalPath(*r.Artifact)

	unlock, err := r.Storage.Lock(*r.Artifact)
	if err != nil {
		return nil, err
	}

	readCloser, err := os.OpenFile(path, os.O_RDONLY, modeReadWriteUser)
	if err != nil {
		return nil, err
	}

	return &lockedReadCloser{
		ReadCloser: readCloser,
		unlock:     unlock,
	}, nil
}

var _ io.ReadCloser = &lockedReadCloser{}

func (r *LocalStorageResourceLocalizationReference) UnpackIntoDirectory(path string) (err error) {
	fi, err := os.Stat(path)
	if err == nil && fi.IsDir() {
		return ErrAlreadyUnpacked
	}

	if err = os.MkdirAll(path, os.ModeDir|os.ModePerm); err != nil {
		return err
	}

	data, err := r.open()
	defer func() {
		err = errors.Join(err, data.Close())
	}()

	return fluxtar.Untar(data, path)
}

func (r *LocalStorageResourceLocalizationReference) GetResource() *v1alpha1.Resource {
	return r.Resource
}

// little helper that allows passing an arbitrary unlock function to the ReadCloser
// that gets called after closing the ReadCloser.
type lockedReadCloser struct {
	io.ReadCloser
	unlock func()
}

func (l *lockedReadCloser) Close() error {
	defer l.unlock()

	return l.ReadCloser.Close()
}

type StaticLocalizationReference struct {
	Path     string
	Resource *v1alpha1.Resource
}

var (
	_ LocalizationSource = &StaticLocalizationReference{}
	_ LocalizationTarget = &StaticLocalizationReference{}
)

func (r *StaticLocalizationReference) Open() (io.ReadCloser, error) {
	return os.Open(r.Path)
}

func (r *StaticLocalizationReference) GetDigest() string {
	return digest.NewDigestFromBytes(digest.SHA256, []byte(r.Path)).String()
}

func (r *StaticLocalizationReference) GetRevision() string {
	return r.Path
}

func (r *StaticLocalizationReference) UnpackIntoDirectory(path string) (err error) {
	fi, err := os.Stat(path)
	if err == nil && fi.IsDir() {
		return ErrAlreadyUnpacked
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

func (r *StaticLocalizationReference) GetResource() *v1alpha1.Resource {
	return r.Resource
}

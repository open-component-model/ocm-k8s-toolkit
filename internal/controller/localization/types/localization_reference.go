package types

import (
	"errors"
	"io"
	"os"

	fluxtar "github.com/fluxcd/pkg/tar"
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

// LocalizationReference is a reference to a resource that can be localized.
// It can be used both as a source (LocalizationSource) and as a target (LocalizationTarget) for localization.
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

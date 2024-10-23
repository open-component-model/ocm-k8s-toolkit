package artifact

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	fluxtar "github.com/fluxcd/pkg/tar"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/util"
)

var (
	ErrAlreadyUnpacked   = errors.New("already unpacked")
	ErrSourceNotYetReady = errors.New("target is not yet ready")
)

// Content is an interface that represents the content of an artifact.
type Content interface {
	// Open returns a reader for instruction. It can be a tarball, a file, etc.
	// The caller is responsible for closing the reader.
	Open() (io.ReadCloser, error)
	// UnpackIntoDirectory unpacks the data into the given directory.
	// It returns an error if the directory already exists.
	// It returns an error if the source cannot be unpacked.
	UnpackIntoDirectory(path string) (err error)

	// RevisionAndDigest returns the revision and digest of the artifact content.
	util.RevisionAndDigest
}

func NewContentBackedByStorageAndResource(
	storage *storage.Storage,
	artifact *artifactv1.Artifact,
	resource *v1alpha1.Resource,
) Content {
	return &ContentBackedByStorageAndResource{
		Storage:  storage,
		Artifact: artifact,
		Resource: resource,
	}
}

type ContentBackedByStorageAndResource struct {
	Storage  *storage.Storage
	Artifact *artifactv1.Artifact
	Resource *v1alpha1.Resource
}

func (r *ContentBackedByStorageAndResource) GetDigest() string {
	return r.Artifact.Spec.Digest
}

func (r *ContentBackedByStorageAndResource) GetRevision() string {
	return r.Artifact.Spec.Revision
}

func (r *ContentBackedByStorageAndResource) Open() (io.ReadCloser, error) {
	return r.open()
}

func (r *ContentBackedByStorageAndResource) open() (io.ReadCloser, error) {
	path := r.Storage.LocalPath(r.Artifact)

	unlock, err := r.Storage.Lock(r.Artifact)
	if err != nil {
		return nil, err
	}

	readCloser, err := os.OpenFile(path, os.O_RDONLY, 0o600)
	if err != nil {
		return nil, err
	}

	return &lockedReadCloser{
		ReadCloser: readCloser,
		unlock:     unlock,
	}, nil
}

var _ io.ReadCloser = &lockedReadCloser{}

func (r *ContentBackedByStorageAndResource) UnpackIntoDirectory(path string) (err error) {
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

func (r *ContentBackedByStorageAndResource) GetResource() *v1alpha1.Resource {
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

func GetContentBackedByStorageAndResource(
	ctx context.Context,
	clnt client.Reader,
	strg *storage.Storage,
	ref meta.NamespacedObjectKindReference,
) (Content, error) {
	if ref.APIVersion == "" {
		ref.APIVersion = v1alpha1.GroupVersion.String()
	}
	if ref.APIVersion != v1alpha1.GroupVersion.String() || ref.Kind != "Resource" {
		return nil, fmt.Errorf("unsupported localization reference type: %s/%s", ref.APIVersion, ref.Kind)
	}

	resource := v1alpha1.Resource{}
	if err := clnt.Get(ctx, client.ObjectKey{
		Namespace: ref.Namespace,
		Name:      ref.Name,
	}, &resource); err != nil {
		return nil, fmt.Errorf("failed to fetch util %s: %w", ref.Name, err)
	}

	if !resource.GetDeletionTimestamp().IsZero() {
		return nil, fmt.Errorf("util %s was marked for deletion and cannot be used, waiting for recreation", resource.Name)
	}

	if !conditions.IsReady(&resource) {
		return nil, fmt.Errorf("%w: util %s is not ready", ErrSourceNotYetReady, resource.Name)
	}

	artifact := artifactv1.Artifact{}
	if err := clnt.Get(ctx, client.ObjectKey{
		Namespace: resource.Namespace,
		Name:      resource.Status.ArtifactRef.Name,
	}, &artifact); err != nil {
		return nil, fmt.Errorf("failed to fetch artifact target %s: %w", resource.Status.ArtifactRef.Name, err)
	}

	if !strg.ArtifactExist(&artifact) {
		return nil, fmt.Errorf("artifact %s specified in component does not exist", artifact.Name)
	}

	return NewContentBackedByStorageAndResource(strg, &artifact, &resource), nil
}

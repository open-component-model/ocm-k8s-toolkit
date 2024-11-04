package artifact

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/containers/image/v5/pkg/compression"
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
	ErrAlreadyUnpacked = errors.New("already unpacked")
	ErrNotYetReady     = errors.New("not yet ready")
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

func NewContentBackedByComponentResourceArtifact(
	storage *storage.Storage,
	component *v1alpha1.Component,
	resource *v1alpha1.Resource,
	artifact *artifactv1.Artifact,
) Content {
	return &ContentBackedByStorageAndComponent{
		Storage:   storage,
		Component: component,
		Resource:  resource,
		Artifact:  artifact,
	}
}

type ContentBackedByStorageAndComponent struct {
	Storage   *storage.Storage
	Component *v1alpha1.Component
	Resource  *v1alpha1.Resource
	Artifact  *artifactv1.Artifact
}

func (r *ContentBackedByStorageAndComponent) GetDigest() (string, error) {
	return r.Artifact.Spec.Digest, nil
}

func (r *ContentBackedByStorageAndComponent) GetRevision() string {
	return fmt.Sprintf(
		"artifact %s in revision %s (from resource %s, based on component %s)",
		r.Artifact.GetName(),
		r.Artifact.Spec.Revision,
		r.Resource.GetName(),
		r.Component.GetName(),
	)
}

func (r *ContentBackedByStorageAndComponent) Open() (io.ReadCloser, error) {
	return r.open()
}

func (r *ContentBackedByStorageAndComponent) open() (io.ReadCloser, error) {
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

func (r *ContentBackedByStorageAndComponent) UnpackIntoDirectory(path string) (err error) {
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

	decompressed, _, err := compression.AutoDecompress(data)
	if err != nil {
		return fmt.Errorf("failed to autodecompress: %w", err)
	}
	defer func() {
		err = errors.Join(err, decompressed.Close())
	}()

	isTar, reader := util.IsTar(decompressed)
	if isTar {
		return fluxtar.Untar(reader, path, fluxtar.WithSkipGzip())
	}

	path = filepath.Join(path, filepath.Base(r.Storage.LocalPath(r.Artifact)))
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to unpack file at %s: %w", path, err)
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()
	if _, err := io.Copy(file, reader); err != nil {
		return fmt.Errorf("failed to copy file to %s: %w", path, err)
	}

	return nil
}

func (r *ContentBackedByStorageAndComponent) GetComponent() *v1alpha1.Component {
	return r.Component
}

func (r *ContentBackedByStorageAndComponent) GetResource() *v1alpha1.Resource {
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

func GetContentBackedByArtifactFromComponent(
	ctx context.Context,
	clnt client.Reader,
	strg *storage.Storage,
	ref meta.NamespacedObjectKindReference,
) (Content, error) {
	if ref.APIVersion == "" {
		ref.APIVersion = v1alpha1.GroupVersion.String()
	}
	if ref.APIVersion != v1alpha1.GroupVersion.String() || ref.Kind != v1alpha1.KindResource {
		return nil, fmt.Errorf("unsupported localization reference type: %s/%s", ref.APIVersion, ref.Kind)
	}

	component, resource, artifact, err := GetComponentResourceArtifactFromReference(ctx, clnt, strg, ref)
	if err != nil {
		return nil, err
	}

	return NewContentBackedByComponentResourceArtifact(strg, component, resource, artifact), nil
}

type ObjectWithTargetReference interface {
	GetTarget() meta.NamespacedObjectKindReference
}

func GetComponentResourceArtifactFromReference(
	ctx context.Context,
	clnt client.Reader,
	strg *storage.Storage,
	ref meta.NamespacedObjectKindReference,
) (*v1alpha1.Component, *v1alpha1.Resource, *artifactv1.Artifact, error) {
	var (
		resource client.Object
		err      error
	)

	switch ref.Kind {
	case v1alpha1.KindLocalizedResource:
		resource = &v1alpha1.LocalizedResource{}
	case v1alpha1.KindConfiguredResource:
		resource = &v1alpha1.ConfiguredResource{}
	case v1alpha1.KindResource:
		resource = &v1alpha1.Resource{}
	default:
		return nil, nil, nil, fmt.Errorf("unsupported reference kind: %s", ref.Kind)
	}

	if err = clnt.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, resource); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch resource %s: %w", ref.Name, err)
	}

	if !resource.GetDeletionTimestamp().IsZero() {
		return nil, nil, nil, fmt.Errorf("resource %s was marked for deletion and cannot be used, waiting for recreation", ref.Name)
	}

	if conditionCheckable, ok := resource.(conditions.Getter); ok {
		if !conditions.IsReady(conditionCheckable) {
			return nil, nil, nil, fmt.Errorf("%w: resource %s", ErrNotYetReady, ref.Name)
		}
	}

	if ref.Kind == v1alpha1.KindResource {
		res := resource.(*v1alpha1.Resource) //nolint:forcetypeassert // we know the type
		component := &v1alpha1.Component{}
		if err = clnt.Get(ctx, client.ObjectKey{
			Namespace: res.GetNamespace(),
			Name:      res.Spec.ComponentRef.Name,
		}, component); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to fetch component %s to which resource %s belongs: %w", res.Spec.ComponentRef.Name, ref.Name, err)
		}

		art := &artifactv1.Artifact{}
		if err = clnt.Get(ctx, client.ObjectKey{
			Namespace: res.GetNamespace(),
			Name:      res.Status.ArtifactRef.Name,
		}, art); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to fetch artifact %s belonging to resource %s: %w", res.Status.ArtifactRef.Name, ref.Name, err)
		}

		return component, res, art, nil
	}

	targetable, ok := resource.(ObjectWithTargetReference)
	if !ok {
		return nil, nil, nil, fmt.Errorf("unsupported reference type: %T", resource)
	}

	return GetComponentResourceArtifactFromReference(ctx, clnt, strg, targetable.GetTarget())
}

// UniqueIDsForArtifactContentCombination returns a set of unique identifiers for the combination of two Content.
// This compromises of
// - the digest of 'a' applied to 'b', machine identifiable and unique
// - the revision of 'a' applied to 'b', human-readable
// - the archive file name of 'a' applied to 'b'.
func UniqueIDsForArtifactContentCombination(a, b Content) (string, string, string, error) {
	revisionAndDigest, err := util.NewMappedRevisionAndDigest(a, b)
	if err != nil {
		return "", "", "", fmt.Errorf("unable to create unique revision and digest: %w", err)
	}
	digest, err := revisionAndDigest.GetDigest()
	if err != nil {
		return "", "", "", fmt.Errorf("unable to get digest: %w", err)
	}

	return digest, revisionAndDigest.GetRevision(), revisionAndDigest.ToArchiveFileName(), nil
}

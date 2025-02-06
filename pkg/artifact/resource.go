package artifact

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/containers/image/v5/pkg/compression"
	"github.com/fluxcd/pkg/runtime/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fluxtar "github.com/fluxcd/pkg/tar"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
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
	registry snapshot.RegistryType,
	component *v1alpha1.Component,
	resource *v1alpha1.Resource,
	snapshot *v1alpha1.Snapshot,
) Content {
	return &ContentBackedByStorageAndComponent{
		Registry:  registry,
		Component: component,
		Resource:  resource,
		Snapshot:  snapshot,
	}
}

type ContentBackedByStorageAndComponent struct {
	Registry  snapshot.RegistryType
	Component *v1alpha1.Component
	Resource  *v1alpha1.Resource
	Snapshot  *v1alpha1.Snapshot
}

func (r *ContentBackedByStorageAndComponent) GetDigest() (string, error) {
	return r.Snapshot.Spec.Blob.Digest, nil
}

func (r *ContentBackedByStorageAndComponent) GetRevision() string {
	// TODO: seems not good
	return fmt.Sprintf(
		"snapshot %s in revision %s (from resource %s, based on component %s)",
		r.Snapshot.GetName(),
		r.Snapshot.Spec.Blob.Digest,
		r.Resource.GetName(),
		r.Component.GetName(),
	)
}

func (r *ContentBackedByStorageAndComponent) Open() (io.ReadCloser, error) {
	return r.open()
}

func (r *ContentBackedByStorageAndComponent) open() (io.ReadCloser, error) {
	ctx := context.Background()
	repository, err := r.Registry.NewRepository(context.Background(), r.Snapshot.Spec.Repository)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	return repository.FetchSnapshot(ctx, r.Snapshot.GetDigest())
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

	// TODO: AutoDecompress only decompresses if data is compressed. Is this still necessary?
	decompressed, _, err := compression.AutoDecompress(data)
	if err != nil {
		return fmt.Errorf("failed to autodecompress: %w", err)
	}
	defer func() {
		err = errors.Join(err, decompressed.Close())
	}()

	// TODO: Check what happens with this early return. Is this still necessary?
	isTar, reader := util.IsTar(decompressed)
	if isTar {
		return fluxtar.Untar(reader, path, fluxtar.WithSkipGzip())
	}

	// TODO: Clean
	return fmt.Errorf("TESTING: it is not a tar")
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

func GetContentBackedBySnapshotFromComponent(
	ctx context.Context,
	clnt client.Client,
	registry snapshot.RegistryType,
	ref *v1alpha1.ConfigurationReference,
) (Content, error) {
	if ref.APIVersion == "" {
		ref.APIVersion = v1alpha1.GroupVersion.String()
	}
	component, resource, artifact, err := GetComponentResourceSnapshotFromReference(ctx, clnt, registry, ref)
	if err != nil {
		return nil, err
	}

	return NewContentBackedByComponentResourceArtifact(registry, component, resource, artifact), nil
}

type ObjectWithTargetReference interface {
	GetTarget() *v1alpha1.ConfigurationReference
}

func GetComponentResourceSnapshotFromReference(
	ctx context.Context,
	clnt client.Reader,
	registry snapshot.RegistryType,
	ref *v1alpha1.ConfigurationReference,
) (*v1alpha1.Component, *v1alpha1.Resource, *v1alpha1.Snapshot, error) {
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

		snapshotResource := &v1alpha1.Snapshot{}
		if err = clnt.Get(ctx, client.ObjectKey{
			Namespace: res.GetNamespace(),
			Name:      res.Status.SnapshotRef.Name,
		}, snapshotResource); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to fetch snapshot %s belonging to resource %s: %w", res.Status.SnapshotRef.Name, ref.Name, err)
		}

		return component, res, snapshotResource, nil
	}

	targetable, ok := resource.(ObjectWithTargetReference)
	if !ok {
		return nil, nil, nil, fmt.Errorf("unsupported reference type: %T", resource)
	}

	return GetComponentResourceSnapshotFromReference(ctx, clnt, registry, targetable.GetTarget())
}

// UniqueIDsForSnapshotContentCombination returns a set of unique identifiers for the combination of two Content.
// This compromises of
// - the digest of 'a' applied to 'b', machine identifiable and unique
// - the revision of 'a' applied to 'b', human-readable
// - the archive file name of 'a' applied to 'b'.
func UniqueIDsForSnapshotContentCombination(a, b Content) (string, string, string, error) {
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

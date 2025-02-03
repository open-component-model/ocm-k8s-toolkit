package mocks

import (
	"context"

	"github.com/opencontainers/go-digest"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
)

type Registry struct{}

var _ snapshot.RegistryType = (*Registry)(nil)

func NewRegistry(_ string) (snapshot.RegistryType, error) {
	return &Registry{}, nil
}

func (r *Registry) NewRepository(ctx context.Context, name string) (snapshot.RepositoryType, error) {
	log.FromContext(ctx).Info("mocking repository creation", "name", name)

	return &Repository{}, nil
}

type Repository struct{}

var _ snapshot.RepositoryType = (*Repository)(nil)

func (r *Repository) PushSnapshot(ctx context.Context, _ string, _ []byte) (digest.Digest, error) {
	log.FromContext(ctx).Info("mocking snapshot push")

	return digest.FromString("mock"), nil
}

func (r *Repository) FetchSnapshot(ctx context.Context, _ string) ([]byte, error) {
	log.FromContext(ctx).Info("mocking snapshot fetch")

	return []byte{}, nil
}

func (r *Repository) DeleteSnapshot(ctx context.Context, _ string) error {
	log.FromContext(ctx).Info("mocking snapshot delete")

	return nil
}

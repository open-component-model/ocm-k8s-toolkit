package snapshot

import (
	"context"
	"errors"

	"oras.land/oras-go/v2/registry/remote"
)

// A RegistryType is something that can create a Repository.
type RegistryType interface {
	NewRepository(ctx context.Context, name string) (RepositoryType, error)
}

type Registry struct {
	*remote.Registry
}

func NewRegistry(url string) (*Registry, error) {
	registry, err := remote.NewRegistry(url)
	if err != nil {
		return nil, err
	}

	return &Registry{registry}, nil
}

func (r *Registry) NewRepository(ctx context.Context, name string) (RepositoryType, error) {
	repository, err := r.Repository(ctx, name)
	if err != nil {
		return nil, err
	}

	remoteRepository, ok := repository.(*remote.Repository)
	if !ok {
		return nil, errors.New("invalid repository type")
	}

	return &Repository{remoteRepository}, nil
}

package localization

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxcd/pkg/runtime/conditions"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/types"
)

var ErrSourceNotYetReady = errors.New("target is not yet ready")

type Client interface {
	client.Reader
	GetLocalizationTarget(ctx context.Context, ref v1alpha1.LocalizationReference) (target types.LocalizationTarget, err error)
	GetLocalizationSource(ctx context.Context, ref v1alpha1.LocalizationSource) (source types.LocalizationSource, err error)
}

func NewClientWithLocalStorage(r client.Reader, s *storage.Storage) Client {
	return &localStorageBackedClient{
		Reader:  r,
		Storage: s,
	}
}

type localStorageBackedClient struct {
	client.Reader
	*storage.Storage
}

var _ Client = &localStorageBackedClient{}

func (clnt *localStorageBackedClient) GetLocalizationTarget(ctx context.Context, ref v1alpha1.LocalizationReference) (target types.LocalizationTarget, err error) {
	switch ref.Kind {
	case "Resource":
		if target, err = clnt.getResourceBasedComponentLocalizationReference(ctx, ref); err != nil {
			err = fmt.Errorf("failed to fetch resource data from resource ref: %w", err)
		}
	default:
		err = fmt.Errorf("unsupported localization target kind: %s", ref.Kind)
	}
	return
}

func (clnt *localStorageBackedClient) GetLocalizationSource(ctx context.Context, ref v1alpha1.LocalizationSource) (source types.LocalizationSource, err error) {
	switch ref.Kind {
	case "Resource":
		var clr *types.ComponentLocalizationReference
		if clr, err = clnt.getResourceBasedComponentLocalizationReference(ctx, ref.LocalizationReference); err != nil {
			err = fmt.Errorf("failed to fetch resource data from resource ref: %w", err)
		}
		source = &types.ComponentLocalizationSource{
			ComponentLocalizationReference: clr,
			Strategy:                       ref.Strategy,
		}
	default:
		err = fmt.Errorf("unsupported localization source kind: %s", ref.Kind)
	}
	return
}

func (clnt *localStorageBackedClient) getResourceBasedComponentLocalizationReference(
	ctx context.Context,
	source v1alpha1.LocalizationReference,
) (*types.ComponentLocalizationReference, error) {
	if source.APIVersion == "" {
		source.APIVersion = v1alpha1.GroupVersion.String()
	}
	if source.APIVersion != v1alpha1.GroupVersion.String() || source.Kind != "Resource" {
		return nil, fmt.Errorf("unsupported localization target reference: %s/%s", source.APIVersion, source.Kind)
	}
	resource := v1alpha1.Resource{}
	if err := clnt.Get(ctx, client.ObjectKey{
		Namespace: source.Namespace,
		Name:      source.Name,
	}, &resource); err != nil {
		return nil, fmt.Errorf("failed to fetch resource %s: %w", source.Name, err)
	}

	if !resource.GetDeletionTimestamp().IsZero() {
		return nil, fmt.Errorf("resource %s was marked for deletion and cannot be used, waiting for recreation", resource.Name)
	}

	if !conditions.IsReady(&resource) {
		return nil, fmt.Errorf("%w: resource %s is not ready", ErrSourceNotYetReady, resource.Name)
	}

	artifact := artifactv1.Artifact{}
	if err := clnt.Get(ctx, client.ObjectKey{
		Namespace: resource.Namespace,
		Name:      resource.Status.ArtifactRef.Name,
	}, &artifact); err != nil {
		return nil, fmt.Errorf("failed to fetch artifact target %s: %w", resource.Status.ArtifactRef.Name, err)
	}

	if !clnt.Storage.ArtifactExist(artifact) {
		return nil, fmt.Errorf("artifact %s specified in component does not exist", artifact.Name)
	}

	return &types.ComponentLocalizationReference{
		LocalArtifactPath: clnt.Storage.LocalPath(artifact),
		Artifact:          &artifact,
	}, nil
}

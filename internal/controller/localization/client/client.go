package client

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
	GetLocalizationSource(ctx context.Context, ref v1alpha1.LocalizationSource) (source types.LocalizationSourceWithStrategy, err error)
	GetComponentLocalizationReferenceFromResource(ctx context.Context, ref v1alpha1.LocalizationReference) (*types.LocalStorageResourceLocalizationReference, error)
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

func (clnt *localStorageBackedClient) GetLocalizationTarget(ctx context.Context, ref v1alpha1.LocalizationReference) (types.LocalizationTarget, error) {
	mapped := map[string]func(context.Context, v1alpha1.LocalizationReference) (*types.LocalStorageResourceLocalizationReference, error){
		"Resource": clnt.GetComponentLocalizationReferenceFromResource,
	}

	if fn, ok := mapped[ref.Kind]; ok {
		return fn(ctx, ref)
	}

	return nil, fmt.Errorf("unsupported localization target kind: %s", ref.Kind)
}

func (clnt *localStorageBackedClient) GetLocalizationSource(ctx context.Context, ref v1alpha1.LocalizationSource) (source types.LocalizationSourceWithStrategy, err error) {
	mapped := map[string]func(context.Context, v1alpha1.LocalizationReference) (*types.LocalStorageResourceLocalizationReference, error){
		"Resource": clnt.GetComponentLocalizationReferenceFromResource,
	}

	if fn, ok := mapped[ref.Kind]; ok {
		clr, err := fn(ctx, ref.LocalizationReference)
		if err != nil {
			return nil, err
		}

		return types.NewLocalizationSourceWithStrategy(clr, ref.Strategy), nil
	}

	return nil, fmt.Errorf("unsupported localization target kind: %s", ref.Kind)
}

func (clnt *localStorageBackedClient) GetComponentLocalizationReferenceFromResource(
	ctx context.Context,
	ref v1alpha1.LocalizationReference,
) (*types.LocalStorageResourceLocalizationReference, error) {
	if ref.APIVersion == "" {
		ref.APIVersion = v1alpha1.GroupVersion.String()
	}
	if ref.APIVersion != v1alpha1.GroupVersion.String() || ref.Kind != "Resource" {
		return nil, fmt.Errorf("unsupported localization target reference: %s/%s", ref.APIVersion, ref.Kind)
	}

	resource := v1alpha1.Resource{}
	if err := clnt.Get(ctx, client.ObjectKey{
		Namespace: ref.Namespace,
		Name:      ref.Name,
	}, &resource); err != nil {
		return nil, fmt.Errorf("failed to fetch resource %s: %w", ref.Name, err)
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

	return &types.LocalStorageResourceLocalizationReference{
		Storage:  clnt.Storage,
		Artifact: &artifact,
		Resource: &resource,
	}, nil
}

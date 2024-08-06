/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ocm

import (
	"context"
	"fmt"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/cpi"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type Contract interface {
	CreateAuthenticatedOCMContext(ctx context.Context, obj *v1alpha1.OCMRepository) (ocm.Context, error)
	GetComponentVersion(ctx context.Context, octx ocm.Context, component *v1alpha1.Component, version string, repoConfig []byte) (cpi.ComponentVersionAccess, error)
}

type Client struct {
	client client.Client
}

func NewClient(client client.Client) *Client {
	return &Client{
		client: client,
	}
}

// CreateAuthenticatedOCMContext provides a context with authentication configured.
func (c *Client) CreateAuthenticatedOCMContext(ctx context.Context, obj *v1alpha1.OCMRepository) (ocm.Context, error) {
	logger := log.FromContext(ctx).WithName("CreateAuthenticatedOCMContext")
	octx := ocm.DefaultContext()

	// If there are no credentials, this call is a no-op.
	if obj.Spec.SecretRef.Name == "" {
		return nil, nil
	}

	repo, err := octx.RepositoryForConfig(obj.Spec.RepositorySpec.Raw, nil)
	if err != nil {
		return nil, fmt.Errorf("ocm repository configuration error: %w", err)
	}
	defer repo.Close()

	if err := ConfigureCredentials(ctx, octx, c.client, obj.Spec.SecretRef.Name, obj.Namespace); err != nil {
		logger.V(v1alpha1.LevelDebug).Error(err, "failed to find credentials")

		// we don't ignore not found errors
		return nil, fmt.Errorf("failed to configure credentials for component: %w", err)
	}

	logger.V(v1alpha1.LevelDebug).Info("credentials configured")

	return octx, nil
}

func (c *Client) GetComponentVersion(ctx context.Context, octx ocm.Context, component *v1alpha1.Component, version string, repoConfig []byte) (cpi.ComponentVersionAccess, error) {
	repo, err := octx.RepositoryForConfig(repoConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("ocm repository configuration error: %w", err)
	}
	defer repo.Close()

	comp, err := repo.LookupComponent(component.Spec.Component)
	if err != nil {
		return nil, fmt.Errorf("ocm component configuration error: %w", err)
	}
	defer comp.Close()

	cv, err := comp.LookupVersion(version)
	if err != nil {
		return nil, fmt.Errorf("failed to find version for component %s, %s: %w", component.Spec.Component, version, err)
	}

	return cv, nil
}

var _ Contract = &Client{}

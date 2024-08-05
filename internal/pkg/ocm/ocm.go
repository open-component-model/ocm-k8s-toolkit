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
)

type Contract interface {
	CreateAuthenticatedOCMContext(ctx context.Context, obj *v1alpha1.OCMRepository) (ocm.Context, error)
	GetComponentVersion(ctx context.Context, octx ocm.Context, component *v1alpha1.Component, version string, repoConfig []byte) (cpi.ComponentVersionAccess, error)
}

type Client struct{}

func NewClient() *Client {
	return &Client{}
}

func (c *Client) CreateAuthenticatedOCMContext(ctx context.Context, obj *v1alpha1.OCMRepository) (ocm.Context, error) {
	return ocm.DefaultContext(), nil
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

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
	"encoding/base64"
	"fmt"
	"sort"

	"github.com/Masterminds/semver/v3"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/cpi"
	"ocm.software/ocm/api/ocm/tools/signing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// Version has two values to be able to sort a list but still return the actual Version.
// The Version might contain a `v`.
type Version struct {
	Semver  *semver.Version
	Version string
}

type Contract interface {
	CreateAuthenticatedOCMContext(ctx context.Context, obj *v1alpha1.OCMRepository) (ocm.Context, error)
	GetComponentVersion(
		ctx context.Context,
		octx ocm.Context,
		name string,
		version string,
		repoConfig []byte,
	) (cpi.ComponentVersionAccess, error)
	GetLatestValidComponentVersion(ctx context.Context, octx ocm.Context, obj *v1alpha1.Component, repoConfig []byte) (string, error)
	ListComponentVersions(logger logr.Logger, octx ocm.Context, obj *v1alpha1.Component, repoConfig []byte) ([]Version, error)
	VerifyComponent(ctx context.Context, octx ocm.Context, obj *v1alpha1.Component, version string, repoConfig []byte) error
}

type Client struct {
	client client.Client
}

func NewClient(client client.Client) *Client {
	return &Client{
		client: client,
	}
}

var _ Contract = &Client{}

// CreateAuthenticatedOCMContext provides a context with authentication configured.
func (c *Client) CreateAuthenticatedOCMContext(ctx context.Context, obj *v1alpha1.OCMRepository) (ocm.Context, error) {
	logger := log.FromContext(ctx).WithName("CreateAuthenticatedOCMContext")
	octx := ocm.DefaultContext()

	// If there are no credentials, this call is a no-op.
	if obj.Spec.SecretRef.Name == "" {
		return octx, nil
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

func (c *Client) VerifyComponent(ctx context.Context, octx ocm.Context, obj *v1alpha1.Component, version string, repoConfig []byte) error {
	logger := log.FromContext(ctx).WithName("VerifyComponent").V(v1alpha1.LevelDebug).WithValues("version", version, "component", obj.Spec.Component)

	logger.Info("fetching component version")
	cv, err := c.GetComponentVersion(ctx, octx, obj.Spec.Component, version, repoConfig)
	if err != nil {
		return fmt.Errorf("failed to get component version: %w", err)
	}
	repo, err := octx.RepositoryForConfig(repoConfig, nil)
	if err != nil {
		return fmt.Errorf("ocm repository configuration error: %w", err)
	}
	defer repo.Close()

	for _, signature := range obj.Spec.Verify {
		logger.Info("checking signature", "signature", signature.Signature)

		var (
			cert []byte
			err  error
		)
		if signature.Value != "" {
			cert, err = base64.StdEncoding.DecodeString(signature.Value)
		} else {
			if signature.SecretRef == "" {
				return fmt.Errorf("kubernetes secret reference not provided")
			}

			cert, err = c.getPublicKey(
				ctx,
				obj.Namespace,
				signature.SecretRef, // name of the secret to find.
				signature.Signature, // the key in the Data Map which holds the right public key for this signature.
			)
		}
		if err != nil {
			return fmt.Errorf("failed to get public key for verification: %w", err)
		}

		logger.Info("retrieved certificate key")

		resolver := ocm.NewCompoundResolver(repo)
		opts := signing.NewOptions(
			signing.Resolver(resolver),
			signing.PublicKey(signature.Signature, cert),
			signing.VerifyDigests(),
			signing.VerifySignature(signature.Signature),
		)
		_, err = signing.VerifyComponentVersion(cv, signature.Signature, opts)
		if err != nil {
			return fmt.Errorf("failed to verify component signature %s: %w", signature.Signature, err)
		}

		logger.Info("successfully verified component signature")
	}

	return nil
}

func (c *Client) getPublicKey(
	ctx context.Context,
	namespace, name, signature string,
) ([]byte, error) {
	var secret corev1.Secret
	secretKey := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}
	if err := c.client.Get(ctx, secretKey, &secret); err != nil {
		return nil, err
	}

	value, ok := secret.Data[signature]
	if !ok {
		return nil, fmt.Errorf("failed to find key %s in secret %s", signature, secretKey)
	}

	return value, nil
}

func (c *Client) GetComponentVersion(
	_ context.Context,
	octx ocm.Context,
	name string,
	version string,
	repoConfig []byte,
) (cpi.ComponentVersionAccess, error) {
	repo, err := octx.RepositoryForConfig(repoConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("ocm repository configuration error: %w", err)
	}
	defer repo.Close()

	cv, err := repo.LookupComponentVersion(name, version)
	if err != nil {
		return nil, fmt.Errorf("failed to find version for component %s, %s: %w", name, version, err)
	}

	return cv, nil
}

// GetLatestValidComponentVersion gets the latest version that still matches the constraint.
func (c *Client) GetLatestValidComponentVersion(
	ctx context.Context,
	octx ocm.Context,
	obj *v1alpha1.Component,
	repoConfig []byte,
) (string, error) {
	logger := log.FromContext(ctx).WithName("GetLatestValidComponentVersion").V(v1alpha1.LevelDebug).WithValues("component", obj.Spec.Component)

	logger.Info("preparing to list component versions")
	versions, err := c.ListComponentVersions(logger, octx, obj, repoConfig)
	if err != nil {
		return "", fmt.Errorf("failed to get component versions: %w", err)
	}

	logger.Info("got versions", "versions", versions)
	if len(versions) == 0 {
		return "", fmt.Errorf("no versions found for component '%s'", obj.Spec.Component)
	}

	sort.SliceStable(versions, func(i, j int) bool {
		return versions[i].Semver.GreaterThan(versions[j].Semver)
	})

	constraint, err := semver.NewConstraint(obj.Spec.Semver)
	if err != nil {
		return "", fmt.Errorf("failed to parse constraint version: %w", err)
	}

	for _, v := range versions {
		if valid, _ := constraint.Validate(v.Semver); valid {
			logger.Info("found valid component version", "version", v.Version)

			return v.Version, nil
		}
	}

	return "", fmt.Errorf("no matching versions found for constraint '%s'", obj.Spec.Semver)
}

func (c *Client) ListComponentVersions(logger logr.Logger, octx ocm.Context, obj *v1alpha1.Component, repoConfig []byte) ([]Version, error) {
	logger = logger.WithName("ListComponentVersions").V(v1alpha1.LevelDebug).WithValues("component", obj.Spec.Component)

	logger.Info("constructing repository")
	repo, err := octx.RepositoryForConfig(repoConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("ocm repository configuration error: %w", err)
	}
	defer repo.Close()

	// get the component Version
	logger.Info("looking for component versions")
	cv, err := repo.LookupComponent(obj.Spec.Component)
	if err != nil {
		return nil, fmt.Errorf("component error: %w", err)
	}
	defer cv.Close()

	logger.Info("listing component versions")
	versions, err := cv.ListVersions()
	if err != nil {
		return nil, fmt.Errorf("failed to list versions for component: %w", err)
	}

	var result []Version
	for _, v := range versions {
		logger.Info("parsing version", "version", v)
		parsed, err := semver.NewVersion(v)
		if err != nil {
			logger.Error(err, "ignoring version as it was invalid semver", "version", v)
			// ignore versions that are invalid semver.
			continue
		}
		result = append(result, Version{
			Semver:  parsed,
			Version: v,
		})
	}

	return result, nil
}

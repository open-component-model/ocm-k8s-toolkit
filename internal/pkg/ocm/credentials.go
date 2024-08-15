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

	corev1 "k8s.io/api/core/v1"
	"ocm.software/ocm/api/credentials/extensions/repositories/dockerconfig"
	"ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/utils/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// ConfigureCredentials takes a repository url and secret ref and configures access to an OCI repository.
func ConfigureCredentials(ctx context.Context, ocmCtx ocm.Context, c client.Client, secretRef, namespace string) error {
	var secret corev1.Secret
	secretKey := client.ObjectKey{
		Namespace: namespace,
		Name:      secretRef,
	}
	if err := c.Get(ctx, secretKey, &secret); err != nil {
		return fmt.Errorf("failed to locate secret: %w", err)
	}

	if dockerConfigBytes, ok := secret.Data[corev1.DockerConfigJsonKey]; ok {
		if err := configureDockerConfigCredentials(ocmCtx, dockerConfigBytes); err != nil {
			return err
		}

		return nil
	}

	if ocmConfigBytes, ok := secret.Data[v1alpha1.OCMConfigKey]; ok {
		if err := configureOcmConfig(ocmCtx, ocmConfigBytes, secret); err != nil {
			return err
		}

		return nil
	}

	return nil
}

func configureOcmConfig(ocmCtx ocm.Context, ocmConfigBytes []byte, secret corev1.Secret) error {
	cfg, err := ocmCtx.ConfigContext().GetConfigForData(ocmConfigBytes, runtime.DefaultYAMLEncoding)
	if err != nil {
		return err
	}

	if err := ocmCtx.ConfigContext().ApplyConfig(cfg, fmt.Sprintf("ocm config secret: %s/%s", secret.Namespace, secret.Name)); err != nil {
		return err
	}

	return nil
}

func configureDockerConfigCredentials(ocmCtx ocm.Context, dockerConfigBytes []byte) error {
	spec := dockerconfig.NewRepositorySpecForConfig(dockerConfigBytes, true)

	if _, err := ocmCtx.CredentialsContext().RepositoryForSpec(spec); err != nil {
		return fmt.Errorf("cannot create credentials from secret: %w", err)
	}

	return nil
}

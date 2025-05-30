---
title: Configuring credentials
description: "Learn how to configure credentials for accessing private OCM repositories in OCM K8s Toolkit resources."
icon: ":key:"
weight:
toc: true
---

# Configuring credentials

OCM K8s Toolkit resources need access to OCM components and their resources. If these OCM components are stored in a
private OCM repository, we need to configure credentials to allow OCM K8s Toolkit resources to access these
repositories.

## How to configure credentials?

Currently, OCM K8s Toolkit supports two ways to configure credentials for accessing private OCM repositories:
- Kubernetes secret of type `dockerconfigjson`
- Kubernetes secret or configmap containing an `.ocmconfig` file.

> [!IMPORTANT]
> Make sure that the secret or configmap containing an OCM config has the correct key to the OCM config file:
> `.ocmconfig`.

### Example: How to configure `.ocmconfig` to access private OCM repositories?

The simplest way to configure credentials for accessing private OCM repositories is to use the `.ocmconfig` file that
was used to transfer the OCM component in the first place.

> [!NOTE]
> For more information on how to create and use the `.ocmconfig` file, please refer to the
> [OCM CLI credentials documentation][ocm-credentials].

For instance, consider you used the following command and `.ocmconfig` file to transfer the OCM component:

```bash
ocm --config ./.ocmconfig transfer ctf ./ctf ghcr.io/<your-namespace>
```

```yaml
type: generic.config.ocm.software/v1
configurations:
  - type: credentials.config.ocm.software
    consumers:
      - identity:
          type: OCIRegistry
          scheme: https
          hostname: ghcr.io
          pathprefix: <your-namespace>
        credentials:
          - type: Credentials
            properties:
              username: <your-username>
              password: <your-password/token>
```

You can now create a secret in the Kubernetes cluster that contains the `.ocmconfig` file:

```bash
kubectl create secret generic ocm-secret --from-file=.ocmconfig
```

> [!NOTE]
> The filename `.ocmconfig` is used as the key in the secret.

### Example: How to configure a Kubernetes secret of type `dockerconfigjson` to access private OCM repositories?

Create a Kubernetes secret of type `dockerconfigjson` that contains the credentials for accessing the private OCM
repository:

```bash
kubectl create secret docker-registry ocm-secret \
  --docker-username=<your-name> \
  --docker-password=<your-password> \
  --docker-server=ghcr.io/<your-namespace>
```

## How to use the configured credentials?

Every OCM K8s Toolkit resource offers a `spec.ocmConfig` field that can be used to specify the credentials for accessing
private OCM repositories. It expects an `OCMConfiguration` that contains a `NamespacedObjectKindReference` to the secret
or configmap that contains the credentials.

```yaml
apiVersion: delivery.ocm.software/v1alpha1
kind: OCMRepository
metadata:
  name: helm-configuration-localization-repository
spec:
  repositorySpec:
    baseUrl: ghcr.io/<your-namespace>
    type: OCIRegistry
  interval: 10m
  ocmConfig:
    - kind: secret
      name: ocm-secret
```

It is possible to specify a propagation policy for the `ocmConfig` field. Setting the policy to `Propagate` will
offer the possibility to reference that resource instead of the secret or configmap again. However, you always need to
specify a reference to the credential either as secret, configmap, or as OCM K8s Toolkit resource for *each resource*.
The credentials will not be propagated automatically to all OCM K8s Toolkit resources in the cluster.

```yaml
apiVersion: delivery.ocm.software/v1alpha1
kind: OCMRepository
metadata:
  name: guide-repository
spec:
  repositorySpec:
    baseUrl: ghcr.io/<your-namespace>
    type: OCIRegistry
  interval: 10m
  ocmConfig:
    - kind: Secret
      name: ocm-secret
      policy: Propagate
---
apiVersion: delivery.ocm.software/v1alpha1
kind: Component
metadata:
  name: guide-component
spec:
  component: ocm.software/ocm-k8s-toolkit/guide-component
  repositoryRef:
    name: guide-repository
  semver: 1.0.0
  interval: 10m
  ocmConfig:
    - kind: OCMRepository
      apiVersion: delivery.ocm.software/v1alpha1
      name: guide-repository
```

The above example shows how to use the `ocmConfig` field in an `OCMRepository` and a `Component`. The `OCMRepository`
references a secret named `ocm-secret` that contains the credentials for accessing the private OCM repository.
The `Component` then references the `OCMRepository` in `ocmConfig`and uses the same credentials.

[ocm-credentials]: https://ocm.software/docs/tutorials/creds-in-ocmconfig/

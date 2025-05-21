# Setup your environment with KinD, kro, and FluxCD

This document describes how to set up a local environment for testing and running examples from the `examples/`
directory or the [getting-started guides](../getting-started).

## Prerequisites

- command line interface, preferably bash, iTerm, wsl, ...
- [kubectl](https://kubernetes.io/docs/tasks/tools/#kubectl)
- [ocm](https://ocm.software/docs/getting-started/installation/) (`ocm` will not be used in this guide, but 
is needed to follow the examples and getting-started guides)

## Start a local Kubernetes cluster with KinD

> [!NOTE]
> You don't need to run KinD if you are using a remote Kubernetes cluster you have access to. If so, you can skip this.

For download and installation instructions, see the [KinD documentation](https://kind.sigs.k8s.io/docs/user/quick-start).

To create a local KinD cluster run the following command:

```bash
kind create cluster
```

## Install kro

Please follow the official installation guides for [kro](https://kro.run/docs/getting-started/Installation). You might
need [helm](https://helm.sh/docs/intro/install/) to install kro.

If kro is installed correctly, you should see some similar output when running the following command:

```bash
kubectl get pods --all-namespaces
```

```console
NAMESPACE            NAME                                         READY   STATUS             RESTARTS        AGE
...
kro                  kro-86d5b5b5bd-6gmvr                         1/1     Running            0               3h28m
...
```

## Install a deployer

Currently, we created our examples and getting-started guides using [FluxCD](https://fluxcd.io/) as deployer.
But, in theory, you could use any other deployer that is able to apply a deployable resource to a Kubernetes cluster,
for instance [ArgoCD](https://argo-cd.readthedocs.io/en/stable/).

To install FluxCD, please follow the official [installation guide](https://fluxcd.io/docs/installation/). After you
installed the cli tool, you can run the following command to install the FluxCD controllers:

```bash
flux install
```

If the FluxCD controllers are installed correctly, you should see some similar output when running the following
command:

```bash
kubectl get pods --all-namespaces
```

```console
NAMESPACE            NAME                                         READY   STATUS             RESTARTS        AGE
...
flux-system          helm-controller-b6767d66-zbwws               1/1     Running            0               3h29m
flux-system          kustomize-controller-57c7ff5596-v6fvr        1/1     Running            0               3h29m
flux-system          notification-controller-58ffd586f7-pr65t     1/1     Running            0               3h29m
flux-system          source-controller-6ff87cb475-2h2lv           1/1     Running            0               3h29m
...
kro                  kro-86d5b5b5bd-6gmvr                         1/1     Running            0               3h28m
...
```

## Install the OCM K8s Toolkit

To install the OCM K8s, you can use one of the following commands:

```bash
# In the ocm-k8s-toolkit/ repository
make deploy
```

or

```bash
kubectl apply -k https://github.com/open-component-model/ocm-k8s-toolkit/config/default?ref=main
```

If the OCM K8s Toolkit controllers are installed correctly, you should see some similar output when running the
following command:

```bash
kubectl get pods --all-namespaces
```

```console
NAMESPACE            NAME                                         READY   STATUS             RESTARTS        AGE
...
flux-system              helm-controller-b6767d66-zbwws                        1/1     Running            0               3h39m
flux-system              kustomize-controller-57c7ff5596-v6fvr                 1/1     Running            0               3h39m
flux-system              notification-controller-58ffd586f7-pr65t              1/1     Running            0               3h39m
flux-system              source-controller-6ff87cb475-2h2lv                    1/1     Running            0               3h39m
...
kro                      kro-86d5b5b5bd-6gmvr                                  1/1     Running            0               3h38m
...
ocm-k8s-toolkit-system   ocm-k8s-toolkit-controller-manager-788f58d4bd-ntbx8   1/1     Running            0               57s
...
```

If all the controllers are running as above you can play around with the examples in the [`examples/`](../../examples) directory or follow
the [getting-started guides](../getting-started).

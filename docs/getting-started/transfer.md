---
title: Transfer OCM components
description: "Transfer OCM components between different remote OCM repositories using the OCM K8s Toolkit."
icon: ":railway_track:"
weight:
toc: true
---

# Transfer OCM components

The replication controller can be used to transfer OCM components between different remote OCM repositories.
This is useful for mirroring components or moving them between environments.

In this guide, we will create an OCM component transfer it to a registry, and then use the OCM K8s Toolkit replication
controller to transfer the component to another registry. As source registry, we will use GitHub's Container registry
and transfer the OCM component to a local registry running in our kind cluster.

To get started, we will have a slightly different setup as described in [here](setup.md), although the
[prerequisites](setup.md#prerequisites) are required either way. To make the local registry available, we need to create
the kind cluster using the following configuration stored in a file called `kind.yaml` (This step is optional if you
already have a Kubernetes cluster or will use a different target registry):

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 31000
        hostPort: 31000
```

Then, create the kind cluster with the following command:

```bash
kind create cluster --config kind.yaml
```

After the cluster is created, you can follow the setup instructions to install the
[OCM K8s Toolkit](setup.md#install-the-ocm-k8s-toolkit).

## Deploy another registry into your Kubernetes cluster (optional)

> [!NOTE]
> This step is optional. If you already have a second registry available that you can use as target registry, you can
> skip this step.

We will deploy a second registry into our Kubernetes cluster to use it as target registry for the transfer.
Create a file called `registry.yaml` and copy the following manifests into it:

```yaml
# Deployment for the zot registry
apiVersion: apps/v1
kind: Deployment
metadata:
  name: registry
spec:
  replicas: 1
  selector:
    matchLabels:
      app: registry
  template:
    metadata:
      labels:
        app: registry
    spec:
      containers:
      - name: registry
        image: ghcr.io/project-zot/zot:latest
        ports:
        - containerPort: 5000
        volumeMounts:
        - name: config-volume
          mountPath: /etc/zot/config.json
          subPath: config.json
      volumes:
      - name: config-volume
        configMap:
          name: config
---
# Configuration for the zot registry
apiVersion: v1
kind: ConfigMap
metadata:
  name: config
data:
  config.json: |
    {
      "storage": {
        "rootDirectory": "/tmp/zot",
        "dedupe": true,
        "gc": true,
        "gcDelay": "1h",
        "gcInterval": "24h"
      },
      "http": {
        "address":"0.0.0.0",
        "port": "5000",
        "compat": "docker2s2"
      }
    }
---
# External port to the image registry. Can be reached from the host with 'localhost:31000'
apiVersion: v1
kind: Service
metadata:
  name: registry-external
  namespace: default
spec:
  type: NodePort
  ports:
    - port: 5000
      targetPort: 5000
      nodePort: 31000
  selector:
    app: registry
---
# Internal port to the image registry. Can be reached from inside the cluster with
# 'http://registry-internal.default.svc.cluster.local:5000/'
apiVersion: v1
kind: Service
metadata:
  name: registry-internal
  namespace: default
spec:
  type: ClusterIP
  ports:
    - port: 5000
      targetPort: 5000
  selector:
    app: registry
```

Now apply the manifest to your Kubernetes cluster:

```bash
kubectl apply -f registry.yaml
```

You can check if the registry is running by executing the following command:

```bash
kubectl wait pod -l app=registry --for condition=Ready --timeout 1m
```

If the registry is running, you should see the following output:

```console
pod/registry-<pod-id> condition met
```

## Create an OCM component

To create the OCM component version, we will use the following `component-constructor.yaml` file:

```yaml
components:
  - name: ocm.software/ocm-k8s-toolkit/replication
    version: "1.0.0"
    provider:
      name: ocm.software
    resources:
      - name: helm-resource
        type: helmChart
        version: "1.0.0"
        access:
          type: ociArtifact
          imageReference: ghcr.io/stefanprodan/charts/podinfo:6.7.1
```

After creating the file, we can create the OCM component version:

```bash
ocm add componentversion --create --file ./ctf component-constructor.yaml
```

> [!NOTE]
> For more details on how to create an OCM component version, please refer to the [OCM documentation][ocm-doc].

This will create a local CTF (Component Transfer Format) directory `./ctf` containing the OCM component version. Since
the OCM component must be accessible for the OCM K8s Toolkit controllers, we will transfer the CTF to a
registry. For this example, we will use GitHub's container registry, but you can use any OCI registry:

```bash
ocm transfer ctf ./ctf ghcr.io/<your-namespace>
```

> [!NOTE]
> If you are using a registry that requires authentication, you need to provide credentials for ocm. Please refer to
> the [OCM CLI credentials documentation][ocm-credentials] for more information on how to set up and use credentials.

If everything went well, you should see the following output:

```bash
ocm get componentversion ghcr.io/<your-namespace>//ocm.software/ocm-k8s-toolkit/replication:1.0.0
```

```console
COMPONENT                                VERSION PROVIDER
ocm.software/ocm-k8s-toolkit/replication 1.0.0   ocm.software
```

## Transfer the OCM component

To transfer the OCM component to another registry, we will use the OCM K8s Toolkit replication controller.
First, we need to create the OCM K8s Toolkit resources by creating a file called `source.yaml` that contains the
respective manifests:

```yaml
# OCMRepository is a basic resource that defines the source registry. The controller will only check if the OCM
# repository exists and is accessible.
apiVersion: delivery.ocm.software/v1alpha1
kind: OCMRepository
metadata:
  name: replication-source-registry
spec:
  interval: 1m0s
  repositorySpec:
    baseUrl: ghcr.io
    subPath: <your-namespace>
    type: OCIRegistry
---
# Component describes the OCM component. The controller will check if the component exists in the referenced OCM
# repository, if the version exists, and verify the component descriptor.
apiVersion: delivery.ocm.software/v1alpha1
kind: Component
metadata:
  name: replication-component
spec:
  component: ocm.software/ocm-k8s-toolkit/replication
  interval: 2m0s
  repositoryRef:
    name: replication-source-registry
  semver: 1.0.0
```

After creating the file, we can apply the OCM K8s Toolkit resources:

```bash
kubectl apply -f source.yaml
```

This will create the resources and the OCM K8s Toolkit controllers will start processing them. You can check the status
of the resources by running:

```bash
kubectl describe ocmrepository replication-source-registry
```

```console
Name:         replication-source-registry
...
Events:
  Type    Reason     Age                  From             Message
  ----    ------     ----                 ----             -------
  Normal  Succeeded  51s (x3 over 2m51s)  ocm-k8s-toolkit  Successfully reconciled
  Normal  Succeeded  51s (x3 over 2m51s)  ocm-k8s-toolkit  Reconciliation finished, next run in 1m0s
```

and

```bash
kubectl describe component replication-component
```

```console
Name:         replication-component
...
Events:
  Type     Reason             Age                    From             Message
  ----     ------             ----                   ----             -------
  Warning  GetResourceFailed  3m43s (x2 over 3m43s)  ocm-k8s-toolkit  OCM Repository is not ready
  Normal   Succeeded          99s (x3 over 3m42s)    ocm-k8s-toolkit  Applied version 1.0.0
  Normal   Succeeded          99s (x3 over 3m42s)    ocm-k8s-toolkit  Reconciliation finished, next run in 2m0s
```

Now we need to create the respective `OCMRepository` resource for the target registry. Create a file called
`target.yaml` with the following content:

```yaml
# OCMRepository is a basic resource that defines the source registry. The controller will only check if the OCM
# repository exists and is accessible.
apiVersion: delivery.ocm.software/v1alpha1
kind: OCMRepository
metadata:
  name: replication-target-registry
spec:
  interval: 1m0s
  repositorySpec:
    # Make sure to use the internal registry URL here, so the OCM K8s Toolkit controllers can access it.
    baseUrl: http://registry-internal.default.svc.cluster.local:5000
    type: OCIRegistry
```

After creating the file, we can apply the OCM K8s Toolkit resources:

```bash
kubectl apply -f target.yaml
```

Check the status of the OCM repository by running:

```bash
kubectl describe ocmrepository replication-target-registry
```

```console
Name:         replication-target-registry
...
Events:
  Type    Reason     Age   From             Message
  ----    ------     ----  ----             -------
  Normal  Succeeded  4s    ocm-k8s-toolkit  Successfully reconciled
  Normal  Succeeded  4s    ocm-k8s-toolkit  Reconciliation finished, next run in 1m0s
```

After creating the OCM K8s Toolkit resources for the source OCM repository and component, as well as the target OCM
repository, we can now create the resource that will connect both repositories and start the transfer. Creat a file
called `replication.yaml` with the following content:

```yaml
apiVersion: delivery.ocm.software/v1alpha1
kind: Replication
metadata:
  name: replication
spec:
  componentRef:
    name: replication-component
  interval: 2m0s
  targetRepositoryRef:
    name: replication-target-registry 
```

Apply the replication resource by executing the following command:

```bash
kubectl apply -f replication.yaml
```

Check the status of the replication by running:

```bash
kubectl describe replication replication
```

```console
Name:         replication
...
Status:
  Conditions:
    Last Transition Time:  2025-05-30T13:01:11Z
    Message:               Successfully replicated replication-component to replication-target-registry
    Observed Generation:   3
    Reason:                Succeeded
    Status:                True
    Type:                  Ready
  History:
    Component:               ocm.software/ocm-k8s-toolkit/replication
    End Time:                2025-05-30T13:01:11Z
    Source Repository Spec:  {"baseUrl":"ghcr.io","subPath":"<your-namespace>","type":"OCIRegistry"}
    Start Time:              2025-05-30T13:01:10Z
    Success:                 true
    Target Repository Spec:  {"baseUrl":"http://registry-internal.default.svc.cluster.local:5000","type":"OCIRegistry"}
    Version:                 1.0.0
  Observed Generation:       3
Events:
  Type    Reason     Age   From             Message
  ----    ------     ----  ----             -------
  Normal  Succeeded  16s   ocm-k8s-toolkit  Successfully replicated replication-component to replication-target-registry
  Normal  Succeeded  16s   ocm-k8s-toolkit  Reconciliation finished, next run in 2m0s
```

Additionally, you can check if the component version is available in the target registry by running:

```bash
ocm get cv http://localhost:31000//ocm.software/ocm-k8s-toolkit/replication
```

```console
 2025-05-30T15:03:17+02:00 warning [ocm/oci/ocireg] "using insecure http for oci registry localhost:31000"
COMPONENT                                VERSION PROVIDER
ocm.software/ocm-k8s-toolkit/replication 1.0.0   ocm.software
```

You should see the component version listed, indicating that the transfer was successful.

This guide has shown how to transfer an OCM component from a source registry to a target registry using the OCM K8s
Toolkit. We created two resources, the `OCMRepository` and `Component`, to define the source OCM repository and its
component. Then, we created another `OCMRepository` for the target registry and finally a `Replication` resource to
connect both repositories and start the transfer. The OCM K8s Toolkit controllers handled the transfer automatically,
ensuring that the component version was replicated to the target registry.

[ocm-doc]: https://ocm.software/docs/getting-started/create-component-version/
[ocm-credentials]: https://ocm.software/docs/tutorials/creds-in-ocmconfig/

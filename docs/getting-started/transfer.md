# Transfer OCM component versions

> [!NOTE]
> This document is under construction. Please refer to https://github.com/open-component-model/ocm-project/issues/487

The replication controller can be used to transfer OCM components between different OCM registries.
This is useful for mirroring components or moving them between environments.

In this guide, we will create an OCM component transfer it to a registry, and then use the OCM K8s Toolkit replication
controller to transfer the component to another registry.

To get started, make sure to setup your environment by fulfilling the [prerequisites](setup.md#prerequisites), start a
local [kind cluster](setup.md#start-a-local-kubernetes-cluster-with-kind) (or have any other running Kubernetes
cluster), installing the [OCM K8s Toolkit](setup.md#install-the-ocm-k8s-toolkit), and having
[access to a registry](setup.md#access-to-a-registry). For simplicity, we will use GitHubs Container Registry as our
source registry, but you can use any other registry.

## Deploy another registry into your Kubernetes cluster (optional)

This step is optional. If you already have a second registry available that you can use as target registry, you can skip
this step.

We will deploy a second registry into our Kubernetes cluster to use it as target registry for the transfer.
Create a file called `registry.yaml` in the root of your repository and copy the following manifests into it:

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
    - port: 5001
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
    - port: 5001
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

This will create a local CTF (Component Transfer Format) directory `./ctf` containing the OCM component version. Since
the OCM component version must be accessible for the OCM K8s Toolkit controllers, we will transfer the CTF to a
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
kubectl apply -f replication.yaml
```

This will create the resources and the OCM K8s Toolkit controllers will start processing them. You can check the status
of the resources by running:

```bash
kubectl describe ocmrepository replication-source-registry
```

```console
Name:         replication-source-registry
...
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






# Deploying a Helm Chart using a `ResourceGraphDefinition` with FluxCD

This guide demonstrates how to deploy a Helm Chart from an OCM component version using OCM K8s Toolkit, kro, and FluxCD.
It is a rather basic example, in which it is assumed that a developer created an application, packages it as a Helm
chart, and publishes it as OCM component version in an OCI registry. Then, an operator who wants to deploy the
application via Helm chart in a Kubernetes cluster, creates a `ResourceGraphDefinition` with resources that point to
this OCM component version and uses FluxCD to configure and deploy it.

Before starting, make sure you have set up your environment as described in the [setup guide](setup.md).

## Create the OCM component version

First, we will create an OCM component version containing a Helm chart. For this example, we will use the `podinfo`
Helm chart, which is a simple web application that serves a pod information page. For more details on how to create an
OCM component version, please refer to the [OCM documentation][ocm-doc].

To create the OCM component version, we will use the following `component-constructor.yaml` file:

```bash
cat > component-constructor.yaml << EOL
components:
  - name: ocm.software/ocm-k8s-toolkit/simple
    provider:
      name: ocm.software
    version: "1.0.0"
    resources:
      - name: helm-resource
        type: helmChart
        version: 1.0.0
        access:
          type: ociArtifact
          imageReference: ghcr.io/stefanprodan/charts/podinfo:6.7.1
EOL
```

and create the OCM component version pointing to the `component-constructor.yaml` file:

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
ocm get componentversion ghcr.io/<your-namespace>//ocm.software/ocm-k8s-toolkit/simple:1.0.0
```

```console
COMPONENT                           VERSION PROVIDER
ocm.software/ocm-k8s-toolkit/simple 1.0.0   ocm.software
```

## Deploy the Helm Chart

### Create the `ResourceGraphDefinition`

### Apply the `ResourceGraphDefinition`

### Create an Instance of ...

[ocm-doc]: https://ocm.software/docs/getting-started/create-component-version/
[ocm-credentials]: https://ocm.software/docs/tutorials/creds-in-ocmconfig/
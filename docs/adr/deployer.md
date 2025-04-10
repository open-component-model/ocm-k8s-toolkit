# Deployment ADR

* Status: proposed
* Deciders: @frewilhelm @Skarlso @fabianburth
* Approvers: 

## Context and Problem Statement

The purpose of the ocm-controllers is to provide a way to deploy resources from an ocm component version. As discussed
in the [artifacts ADR](./artifacts.md#negative-consequences) this requires some kind of deployer that consumes the
given resource and uses it to create some kind of deployment.

In the current scope the deployer must be able to deploy a resource, which can be a Helm chart, a Kustomization, or a
plain Kubernetes resource. It will be deployed to a Kubernetes cluster using FluxCD. The resource must be provided as
OCI artifact that FluxCDs `OCIRepository` can consume.

The resource provides a status that holds a reference to an OCI artifact and its manifest. The deployer must be
able to get the OCI artifact reference through a reference to that resource by accessing the status of the resource.

The deployer must make it easy for the user to deploy the resources from the ocm component version. It is easy if the
structure of the deployer is simple and easy to understand, e.g. not requiring a lot of different resources for a
deployment (like `OCMRepository`, `Component`, `Resource`, `OCIRepository`, `HelmRelease`, `Kustomization`, ...).

However, the deployer must also be flexible enough to deploy different kinds of resources and reference several
resources of an ocm component version. It must be possible to configure these resources and dynamically configure
the resource that is deployed, while deploying it. For example, injecting cluster-names or substituting values in the
to-be-deployed resources.

## Decision Drivers

* The simpler, the better, but flexible enough for all our use-cases.
* Currently, our focus is on using FluxCD to deploy resources and use FluxCDs `OCIRepository` to provide our resources 
for deployment as we store the resources internally in an OCI registry.

## Considered Options

* [Kro](#kro): Use Kro's `ResourceGraphDefinition` to orchestrate all required resources (from OCM Kubernetes-resources to
  the deployment resources).
* [FluxDeployer](#fluxdeployer): Create a CRD `FluxDeployer` that points to the consumable resource and its reconciler 
creates FluxCDs `OCIRepository` and `HelmRelease`/`Kustomization` to create the deployment based on the resource.
(This option would be closest to the `ocm-controllers` v1 implementation.)
* [Deployer](#deployer): Create a CRD `Deployment` that is a wrapper for all resources that are required for the deployment.

## Decision Outcome

Chosen option: "[???]", because 

### Positive Consequences

* [e.g., improvement of quality attribute satisfaction, follow-up decisions required, …]
* …

### Negative Consequences

* [e.g., compromising quality attribute, follow-up decisions required, …]
* …

## Pros and Cons of the Options

### Kro

> [!IMPORTANT]
> This approach requires a basic understanding of [kro][kro-github]! Please read the [documentation][kro-doc] before
> proceeding.

Using Kro, developers or operators can define a `ResourceGraphDefinition` with deployment instructions which
orchestrates all required resources for the deployment of a resource from an OCM component version. The deployment can
be very flexible by using CEL expression to configure the resources based on other resources or values from the
instance.

#### A simple use case

A simple use case is an OCM component version containing a Helm chart and using a `ResourceGraphDefinition` to create
the deployment.

![ocm-controller-deployer](../assets/ocm-controller-deployer.svg)

The flowchart shows an OCM repository that contains an OCM component version. This OCM component version holds a Helm
chart. By creating a `ResourceGraphDefinition` with the respective resources referring each other accordingly, the
deployment of that Helm chart can be orchestrated by creating an instance of the resulting CRD `Simple`.

The manifests for such a use case could look like this:

`component-constructor.yaml`
```yaml
components:
  - name: ocm.software/ocm-k8s-toolkit/helm-simple
    version: "1.0.0"
    provider:
      name: ocm.software
    resources:
      # This helm resource contains additional deployment instructions for the application itself
      - name: helm-resource
        type: helmChart
        version: "1.0.0"
        access:
           type: ociArtifact
           imageReference: ghcr.io/stefanprodan/charts/podinfo:6.7.1
```

`resource-graph-definition.yaml`
```yaml
apiVersion: kro.run/v1alpha1
kind: ResourceGraphDefinition
metadata:
  name: helm-simple-rgd
spec:
  schema:
    apiVersion: v1alpha1
    # CRD that gets created
    kind: Simple
    # Values that can be configured using Kro instance (configuration) (= passed through the instance)
    spec:
      releaseName: string | default="helm-simple"
  resources:
    - id: ocmRepository
      template:
        apiVersion: delivery.ocm.software/v1alpha1
        kind: OCMRepository
        metadata:
          name: "helm-simple-ocmrepository"
        spec:
          repositorySpec:
            baseUrl: ghcr.io/<your-org>
            type: OCIRegistry
          interval: 10m
    - id: component
      template:
        apiVersion: delivery.ocm.software/v1alpha1
        kind: Component
        metadata:
          name: "helm-simple-component"
        spec:
          component: ocm.software/ocm-k8s-toolkit/helm-simple
          repositoryRef:
            name: "${ocmRepository.metadata.name}"
          semver: 1.0.0
          interval: 10m
    - id: resourceChart
      template:
        apiVersion: delivery.ocm.software/v1alpha1
        kind: Resource
        metadata:
          name: "helm-simple-resource-chart"
        spec:
          componentRef:
            name: ${component.metadata.name}
          resource:
            byReference:
              resource:
                name: helm-resource
          interval: 10m
    - id: ocirepository
      template:
        apiVersion: source.toolkit.fluxcd.io/v1beta2
        kind: OCIRepository
        metadata:
          name: "helm-simple-ocirepository"
        spec:
          interval: 1m0s
          layerSelector:
            mediaType: "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
            operation: copy
          # Use values from the resource "resourceChart"
          # A resource reconciled by the resource-controller will provide a SourceReference in its status (if possible)
          #    type SourceReference struct {
          #      Registry   string `json:"registry"`
          #      Repository string `json:"repository"`
          #      Reference  string `json:"reference"`
          #    }
          url: oci://${resourceChart.status.reference.registry}/${resourceChart.status.reference.repository}
          ref:
            tag: ${resourceChart.status.reference.reference}
    - id: helmrelease
      template:
        apiVersion: helm.toolkit.fluxcd.io/v2
        kind: HelmRelease
        metadata:
          name: "helm-simple-helm-release"
        spec:
          # Configuration (passed through Kro instance, respectively, the custom resource "AdrInstance")
          releaseName: ${schema.spec.releaseName}
          interval: 1m
          timeout: 5m
          chartRef:
            kind: OCIRepository
            name: ${ocirepository.metadata.name}
            namespace: default
```

`instance.yaml`
```yaml
# The instance of the CRD created by the ResourceGraphDefinition
apiVersion: kro.run/v1alpha1
kind: Simple
metadata:
  name: helm-simple-instance
spec:
  # Pass values for configuration
  releaseName: "helm-simple-instance"
```

#### A more complex use case with bootstrapping

It is possible to ship the `ResourceGraphDefinition` with the OCM component version itself. This, however, requires some
kind of bootstrapping as the `ResourceGraphDefinition` must be sourced from the component version and applied to the
cluster.

To bootstrap a `ResourceGraphDefinition` an operator is required, e.g. `OCMDeployer`, that takes a Kubernetes custom
resource (OCM) `resource`, extracts the `ResourceGraphDefinition` from the component version and applies it to the
cluster.

The developer can define any localisation or configuration directive in the `ResourceGraphDefinition`. The operator only
has to deploy an instance of the CRD that is created by the `ResourceGraphDefinition` and pass values to its scheme if
necessary.

![ocm-controller-deployer-bootstrap](../assets/ocm-controller-deployer-bootstrap.svg)

The flowchart shows a git repository containing the application source code for the image, the according Helm chart,
the component constructor, and a `ResourceGraphDefinition`. In the example, all these components are stored in an OCM
component version and transferred to an OCM repository.

Then, several resources for bootstrapping are deployed into the cluster, which refer to the created OCM component
version, take the `ResourceGraphDefinition`, and apply that manifest. In comparison to the simple use case, this
`ResourceGraphDefinition` contains the image of the application. The `resource.status.sourceReference` field is used to
localise the image using FluxCDs `HelmRelease.spec.values` field since the location-reference of that image was adjusted
while transferring the image to its new location using OCM. As a result, the image-reference in the Helm chart now
points to the new location of the image.

The following manifests show an example of such a setup:

`component-constructor.yaml`
```yaml
components:
  - name: ocm.software/ocm-k8s-toolkit/helm-bootstrap
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
      # This image resource contains the application image (can be used for localisation while deploying)
      - name: image-resource
        type: ociArtifact
        version: "1.0.0"
        access:
          type: ociArtifact
          imageReference: ghcr.io/stefanprodan/podinfo:6.7.1
      # This resource contains the kro ResourceGraphDefinition
      - name: kro-rgd
        type: blob
        version: "1.0.0"
        input:
          type: file
          path: ./resource-graph-definition.yaml
```

`resource-graph-definition.yaml`
```yaml
apiVersion: kro.run/v1alpha1
kind: ResourceGraphDefinition
metadata:
  name: bootstrap-rgd
spec:
  schema:
    apiVersion: v1alpha1
    kind: Bootstrap
    spec:
      releaseName: string | default="bootstrap-release"
      message: string | default="hello world"
  resources:
    - id: resourceChart
      template:
        apiVersion: delivery.ocm.software/v1alpha1
        kind: Resource
        metadata:
          name: helm-chart
        spec:
          componentRef:
            name: component-name
          resource:
            byReference:
              resource:
                name: helm-resource
          interval: 10m
    # Kro resource that is used to do localisation
    - id: resourceImage
      template:
        apiVersion: delivery.ocm.software/v1alpha1
        kind: Resource
        metadata:
          name: image
        spec:
          componentRef:
            name: component-name
          resource:
            byReference:
              resource:
                name: image-resource
          interval: 10m
    # Any deployer can be used. In this case we are using FluxCD HelmRelease that references FluxCD OCIRepository
    - id: ocirepository
      template:
        apiVersion: source.toolkit.fluxcd.io/v1beta2
        kind: OCIRepository
        metadata:
          name: oci-repository
        spec:
          interval: 1m0s
          layerSelector:
            mediaType: "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
            operation: copy
          url: oci://${resourceChart.status.reference.registry}/${resourceChart.status.reference.repository}
          ref:
            tag: ${resourceChart.status.reference.reference}
    - id: helmrelease
      template:
        apiVersion: helm.toolkit.fluxcd.io/v2
        kind: HelmRelease
        metadata:
          name: helm-release
        spec:
          releaseName: ${schema.spec.releaseName}
          interval: 1m
          timeout: 5m
          chartRef:
            kind: OCIRepository
            name: ${ocirepository.metadata.name}
            namespace: default
          values:
            # Localisation (image location is adjusted to its location through OCM transfer)
            image:
              repository: ${resourceImage.status.reference.registry}/${resourceImage.status.reference.repository}
              tag: ${resourceImage.status.reference.reference}
            ui:
              message: ${schema.spec.message}
```

`bootstrap.yaml`
```yaml
# OCMRepository contains information about the location where the component version is stored
apiVersion: delivery.ocm.software/v1alpha1
kind: OCMRepository
metadata:
  name: repository-name
spec:
  repositorySpec:
    baseUrl: ghcr.io/<your-org>
    type: OCIRegistry
  interval: 10m
---
# Component contains information about which component version to use
apiVersion: delivery.ocm.software/v1alpha1
kind: Component
metadata:
  name: component-name
spec:
  # Reference to the component version used above
  component: ocm.software/ocm-k8s-toolkit/helm-bootstrap
  repositoryRef:
    name: repository-name
  semver: 1.0.0
  interval: 10m
---
# ResourceGraphDefinition to orchestrate the deployment
apiVersion: delivery.ocm.software/v1alpha1
kind: Resource
metadata:
  name: resource-rgd-name
spec:
  componentRef:
    name: component-name
  resource:
    byReference:
      resource:
        # Reference to the resource name in the component version
        name: kro-rgd
  interval: 10m
---
# Operator to deploy the ResourceGraphDefinition
apiVersion: delivery.ocm.software/v1alpha1
kind: OCMDeployer
metadata:
  name: ocmdeployer-name
spec:
  resourceRef:
    name: resource-rgd-name
  interval: 10m
```

`instance.yaml`
```yaml
apiVersion: kro.run/v1alpha1
kind: Bootstrap
metadata:
  name: helm-bootstrap
spec:
  releaseName: "bootstrap-instance"
  message: "Hello from the instance!"
```



#### Pros

* Kro and FluxCD provide the same functionality as the configuration and localisation controller. But instead of
  localising and configuring the resource and then storing it somewhere to be processed, the resource is configured and
  localised within the `ResourceGraphDefinition` and FluxCDs `HelmRelease.spec.values` or `Kustomization.spec.path`.
  Accordingly, the controllers could be omitted.
  * As a result, the internal storage can be omitted as well as we do not need to download the resources to
    configure or localise them and make them available again.
    * By omitting the internal storage, we can omit the storage implementation.
* The codebase would get simpler as the deployment logic is outsourced
* With Kros `ResourceGraphDefinition` the developer can create the deployment-instructions and pack them into the
  component version.
* The developer can use any kind of deployer (FluxCD, ArgoCD, ...) that fits their needs by specifying the
  `ResourceGraphDefinition` accordingly.

#### Cons

* Kro is a new dependency and is not production-ready and [APIs may change][kro-alpha-stage]. We already found some
  issues. However, the project is open for contributions and some are already accepted:
  * [No possibility to refer to properties within a property of type `json`](https://github.com/open-component-model/ocm-project/issues/455): Open
  * [Kro does not reconcile instances on `ResourceGraphDefinition` changes](https://github.com/open-component-model/ocm-project/issues/451): Fixed
  * [Instances of an RGD are not deleted on RGD-deletion](https://kubernetes.slack.com/archives/C081TMY9D6Y/p1744098078849929): Open
  * Deletion handling in general
    * Example 1: I create resource `a` in a cluster. Then, I create a RGD with the same resource `a` and its instance.
      Now, when I delete my instance, resource `a` is also deleted, although, the original manifest of resource `a` that
      I applied previously was not changed.
    * Example 2: I create an RGD with the same resource `a`. Then, I create two instances of that RGD. Now, when I
      delete one instance, resource `a` is deleted, even though the other instance is still present.
  * Missing ownership of resources created by RGD/instance 
  * How are updates of resources handled?
  * What about drift detection of instances?
  * What happens if the `ResourceGraphDefinition`-scheme changes (when an instance is already deployed)?
    * Local testing: We created a `ResourceGraphDefinition` with a `schema.spec`-field of type `string` and deployed the
      RGD as well as an instance of that. Afterwards, we changed the field type to `integer` and reapplied the RGD
      manifest.
      The RGD was reconciled and updated the CRD. The instance did not error, but editing the instance by using the old
      datatype was not possible (usual error of a wrong datatype). However, the original value (of type `string`) was
      still present.
    * How is the migration path?
  * It is not possible reference external resources like `configmap[namespace/name].data`.
    * Already a ["Mega Feature" (request)](https://github.com/kro-run/kro/issues/72), but still open.

### FluxDeployer

_Description_

#### Pros

* ...

#### Cons

* ...

### Deployer

_Description_

#### Pros

* ...

#### Cons

* ...


# Links
- Epic [#404](https://github.com/open-component-model/ocm-k8s-toolkit/issues/147)
- Issue [#90](https://github.com/open-component-model/ocm-k8s-toolkit/issues/136)

[kro-github]: https://github.com/kro-run/kro
[kro-doc]: https://kro.run/docs/overview 
[kro-alpha-stage]: https://github.com/kro-run/kro/blob/965cb76668433742033c0e413e3cc756ef86d89a/website/docs/docs/getting-started/01-Installation.md?plain=1#L21
[fluxcd-helmrelease-values]: https://fluxcd.io/flux/components/helm/helmreleases/#values
[fluxcd-kustomization-patches]: https://fluxcd.io/flux/components/kustomize/kustomizations/#patches

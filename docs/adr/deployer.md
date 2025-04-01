# Deployment ADR

* Status: proposed
* Deciders: @frewilhelm @ikhandamirov @Skarlso
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

Kube Resource Orchestrator ([kro][kro-github]) is an open-source Kubernetes operator designed to simplify the creation
and management of complex resource configurations within Kubernetes clusters. At the heart of kro is the
`ResourceGraphDefinition`, a custom resource that specifies a collection of Kubernetes resources and their
interdependencies. This definition allows for the encapsulation of complex resource groupings into reusable components,
streamlining deployment processes.

When a `ResourceGraphDefinition` is applied to a Kubernetes cluster, the kro controller validates its specifications.
Upon validation, kro dynamically generates a new Custom Resource Definition (CRD) and registers it with the Kubernetes
API server, effectively extending the Kubernetes API to include the new resource type.
Kro then deploys a dedicated controller tailored to manage instances of the newly created CRD. This microcontroller is
responsible for overseeing the lifecycle of the resources defined within the `ResourceGraphDefinition`.
When an `instance` of the custom resource is created, the microcontroller interprets the instance parameters,
orchestrates the creation and configuration of the underlying resources, and manages their states to ensure alignment
with the desired specifications.

kro integrates the Common Expression Language (CEL) to enable dynamic resource configuration. In its
`ResourceGraphDefinition`, CEL expressions are used to establish dependencies and reference values between resources.
For instance, a CEL expression can extract an output from one resource and use it as an input for another, ensuring that
interdependent resources are correctly configured and deployed in the appropriate sequence.

Accordingly, the `ResourceGraphDefinition` provides a high-level abstraction for defining complex resources and is
flexible enough to accommodate a wide range of deployment scenarios. It gives the possibility to configure resources
dynamically, which is a key requirement for the deployment of resources from an OCM component version. In addition with
FluxCDs `HelmRelease` [capability][fluxcd-helmrelease-values] to replace values using `spec.values` or FluxCDs 
`Kustomzation` [capability][fluxcd-kustomization-patches] to replace values using `spec.patches` the localisation
functionality is also provided.


TODO notes:
- Omit localisation and configuration
  - If this is omitted, we can omit the internal registry as well

#### Pros

* ...

#### Cons

* ...

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
[fluxcd-helmrelease-values]: https://fluxcd.io/flux/components/helm/helmreleases/#values
[fluxcd-kustomization-patches]: https://fluxcd.io/flux/components/kustomize/kustomizations/#patches

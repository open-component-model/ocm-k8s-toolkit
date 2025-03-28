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

* [FluxDeployer](#fluxdeployer): Create a CRD `FluxDeployer` that points to the consumable resource and its reconciler 
creates FluxCDs `OCIRepository` and `HelmRelease`/`Kustomization` to create the deployment based on the resource.
(This option would be closest to the `ocm-controllers` v1 implementation.)
* [Deployer](#deployer): Create a CRD `Deployment` that is a wrapper for all resources that are required for the deployment.
* [Kro](#kro): Use Kro's `ResourceGraphDefinition` to orchestrate all required resources (from OCM Kubernetes-resources to
the deployment resources).

## Decision Outcome

Chosen option: "[???]", because 

### Positive Consequences

* [e.g., improvement of quality attribute satisfaction, follow-up decisions required, …]
* …

### Negative Consequences

* [e.g., compromising quality attribute, follow-up decisions required, …]
* …

## Pros and Cons of the Options

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

### Kro 

_Description_

#### Pros

* ...

#### Cons

* ...


# Links
- Epic [#404](https://github.com/open-component-model/ocm-k8s-toolkit/issues/147)
- Issue [#90](https://github.com/open-component-model/ocm-k8s-toolkit/issues/136)

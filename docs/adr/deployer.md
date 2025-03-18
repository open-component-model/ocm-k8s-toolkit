# Deployment ADR

* Status: proposed
* Deciders: @frewilhelm @ikhandamirov @Skarlso
* Approvers: 

## Context and Problem Statement

The purpose of the ocm-controllers is to provide a way to deploy resources from an ocm component version. As discussed
in the [artifacts ADR](./artifacts.md#negative-consequences) this requires some kind of deployer that consumes the
given resource and uses it to create some kind of deployment.

## Decision Drivers

* The simpler, the better 
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
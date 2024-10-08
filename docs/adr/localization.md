# Localization

* Status: proposed
* Deciders: Fabian Burth, Uwe Krueger, Gergely Brautigam, Jakob Moeller
* Date: 2024-10-08

Technical Story:

The term *localization* was termed in the context of the 
[open component model](https://ocm.software/) for the process of adjusting
the resource locations specified in deployment instructions to a particular
target environment.  

Currently, the primary use case is kubernetes as target runtime environment, and
therefore, the typical deployment instructions are kubernetes manifests, helm
charts or kustomization overlays.

Example:

Assume we have the following component in a public oci registry:

```yaml
apiVersion: ocm.software/v3alpha1 
kind: ComponentVersion
metadata:
  name: github.com/open-component-model/myapp
  provider: 
    name: ocm
  version: v1.0.0 
repositoryContexts: 
- baseUrl: ghcr.io
  componentNameMapping: urlPath
  subPath: open-component-model
  type: OCIRegistry
spec:
  resources: 
  - name: myimage 
    relation: external 
    type: ociImage 
    version: v1.0.0
    access: 
      type: ociArtifact 
      imageReference: ghcr.io/open-component-model/myimage
  - name: mydeploymentinstruction
    relation: external
    type: k8s-manifest
    version: v1.0.0
    access:
      type: ociArtifact
      imageReference: ghcr.io/open-component-model/mydeploymentinstruction:v1.0.0
```

The kubernetes manifest (called `mydeploymentinstruction` here) looks like this:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: mypod
spec:
  containers:
  - name: mypod
    image: ghcr.io/open-component-model/mydeploymentinstruction:v1.0.0
```

Now, we transfer the component and copy all resources into a private registry in 
our environment at myprivateregistry.com. After that, the component looks like 
this:

```yaml
apiVersion: ocm.software/v3alpha1
kind: ComponentVersion
metadata:
  name: github.com/open-component-model/myapp
  provider:
    name: ocm
  version: v1.0.0
repositoryContexts:
  - baseUrl: ghcr.io
    componentNameMapping: urlPath
    subPath: open-component-model
    type: OCIRegistry
  - baseUrl: myprivateregistry.com
    componentNameMapping: urlPath
    subPath: open-component-model
    type: OCIRegistry
spec:
  resources:
    - name: myimage
      relation: external
      type: ociImage
      version: v1.0.0
      access:
        type: ociArtifact
        imageReference: myprivateregistry.com/open-component-model/myimage:v1.0.0
    - name: mydeploymentinstruction
      relation: external
      type: k8s-manifest
      version: v1.0.0
      access:
        type: ociArtifact
        imageReference: myprivateregistry.com/open-component-model/mydeploymentinstruction:v1.0.0
```

The resources - and thus, also the kubernetes manifest called 
`mydeploymentinstruction` - remain unchanged. There are really just copied
without any mutation. Consequently, the pod image reference still points to the
old image location before the transfer 
(ghcr.io/open-component-model/mydeploymentinstruction:v1.0.0)
instead of to the new image location after transfer (myprivateregistry.com/open-component-model/myimage)
that's also specified in the `myimage` resource.

So, it looks like this:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: mypod
spec:
  containers:
    - name: mypod
      image: ghcr.io/open-component-model/mydeploymentinstruction:v1.0.0
```

But this would be an issue - especially if the target environment does not have
access to the source environment (thus, if ghcr.io/open-component-model/mydeploymentinstruction:v1.0.0
is not reachable from the target environment).

So instead, the image references in the `mydeploymentinstruction` resource have
to be adjusted to the image reference in the `myimage` resource:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: mypod
spec:
  containers:
    - name: mypod
      image: myprivateregistry.com/open-component-model/myimage:v1.0.0
```

This adjustment is what we call *localization*. 

## Context and Problem Statement

The above described process should now be automated for the context of the
*ocm-k8s-toolkit* to enable "ocmops" based continuous deployments based on ocm
components.

Multiple different solutions have been suggested.

## Decision Drivers <!-- optional -->

**Driver 1:** User Experience.

* [driver 1, e.g., a force, facing concern, …]
* [driver 2, e.g., a force, facing concern, …]
* … <!-- numbers of drivers can vary -->

## Considered Options

**Option 1 - Pre-Processing / Templating**

**Option 2 - Literal Substitution Rules**
Generate literal substitution rules as manifest for the localization controller
based on templates embedded in the component.

**Option 3 - Component-Based Subsitution Rules (previous solution)**
https://github.com/open-component-model/ocm-controller/blob/main/docs/architecture.md#localization-controller

**Option 4 - Substitution With Mutating Webhooks**

* [option 1]
* [option 2]
* [option 3]
* … <!-- numbers of options can vary -->

## Decision Outcome

Chosen option: "[option 1]", because [justification. e.g., only option, which meets k.o. criterion decision driver | which resolves force force | … | comes out best (see below)].

### Positive Consequences <!-- optional -->

* [e.g., improvement of quality attribute satisfaction, follow-up decisions required, …]
* …

### Negative Consequences <!-- optional -->

* [e.g., compromising quality attribute, follow-up decisions required, …]
* …

## Pros and Cons of the Options <!-- optional -->

### Option 1 - Pre-Processing / Templating

[example | description | pointer to more information | …] <!-- optional -->

* Good, because the component is not invalid/inconsistent after the transport.
* Bad, because the deployment instructions are not more valid manifest / helm
charts / kustomize overlays.
* … <!-- numbers of pros and cons can vary -->

### [option 2]

[example | description | pointer to more information | …] <!-- optional -->

* Good, because [argument a]
* Good, because [argument b]
* Bad, because [argument c]
* … <!-- numbers of pros and cons can vary -->

### [option 3]

[example | description | pointer to more information | …] <!-- optional -->

* Good, because [argument a]
* Good, because [argument b]
* Bad, because [argument c]
* … <!-- numbers of pros and cons can vary -->

## Links <!-- optional -->

* [Link type] [Link to ADR] <!-- example: Refined by [ADR-0005](0005-example.md) -->
* … <!-- numbers of links can vary -->

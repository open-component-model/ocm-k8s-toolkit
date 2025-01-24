# Use Single Layer OCI Artifacts for (intermediate) blobs

* Status: proposed
* Deciders: @frewilhelm @ikhandamirov
* Approvers: 

Technical Story: https://github.com/open-component-model/ocm-project/issues/333

## Context and Problem Statement

The controllers in this repository create artifacts/blobs that are used by one another. For example, the
component-controller creates an artifact containing the component descriptors from the specified component version.
Finally, the resource controller, or if specified the configuration controller, creates a blob as an artifact that holds
the resource that is consumed by the deployers.

Initially, it was planned to use a Custom Resource `artifact` type to represent these artifacts.
This `artifact` type was [defined][artifact-definition] to point to a URL and holds a "human-readable" identifier
`Revision` of a blob stored in a http-server inside the controller.

The `artifact` idea was part of a bigger [RFC][fluxcd-rfc] for `FluxCD`. Unfortunately, the change would be difficult
to communicate and potentially prompt security audits on FluxCDs customer side. Thus, the proposal was not acceptable
in the given format due to the differences on the watch on the `artifact` resource. This was tantamount to a rejection.

Therefore, the original purpose of that Custom Resource `artifact` is not present anymore. Additionally, the team
decided to not use a plain http-server but an internal OCI registry to store and publish its blobs that are produced by
the OCM controllers as single layer OCI artifacts.

Arguments (meeting notes from 26.11.2024):
- An OCI registry is a responsibility less to maintain 
- Stop support at the level of the distribution spec of OCI
- OCI registries could provide GC
- We will need an abstraction that handles OCI registries anyway

The following discussion concerns two major topics:
- How to store and reference the single layer OCI artifacts.
- How to setup the internal OCI registry and which one to use.

## Decision Drivers

- Reduce maintenance effort
- Fit into our use-cases (especially with FluxCD)

## Artifact (How to store and reference the single layer OCI artifacts)

An artifact in the current context describes a resource that holds an identity to a blob and a pointer where to find
the blob (currently a URL). In that sense, a producer can create an artifact and store this information and a consumer
can search for artifacts with the specific identity to find out its location.

In the current implementation the artifact is defined in the [openfluxcd/artifact repository][artifact-definition].

The ocm-controller `v1` implementation defined a `snapshot` type that serves similar purposes.
Its definition can be found in [open-component-model/ocm-controller][snapshot-definition].

### Comparison `artifact` vs `snapshot`

To enable the following option discussion, the fields of the CRs `artifact` and `snapshot` are compared:

#### Snapshot

From [ocm-controller v1 Architecture][ocm-controller-v1-architecture]:
_snapshots are immutable, Flux-compatible, single layer OCI images containing a single OCM resource.
Snapshots are stored in an in-cluster registry and in addition to making component resources accessible for
transformation, they also can be used as a caching mechanism to reduce unnecessary calls to the source OCM registry._

[`SnapshotSpec`][snapshot-spec]
- Identity: OCM Identity (map[string]string) (Created by [constructIdentity()][snapshot-create-identity])
- Digest: OCI Layer Digest (Based on [go-containerregistry OCI implementation][go-containerregistry-digest])
- Tag: The version (e.g. `latest`, `v1.0.0`, ..., see [reference][snapshot-version-ref]
- (Suspend)

[`SnapshotStatus`][snapshot-status]
- (Conditions)
- LastReconciledDigest:
  - Purpose?
- LastReconciledTag:
  - Purpose?
- RepositoryURL: Concrete URL pointing to the local registry including the service name 
- (ObservedGeneration)

#### Artifact

[`ArtifactSpec`][artifact-spec]
- URL: HTTP address of the artifact as exposed by the controller managing the source
- Revision: "Human-readable" identifier traceable in the origin source system (commit SHA, tag, version, ...)
- Digest: Digest of the file that is stored (algo:checksum)
  - Used to verify the artifact (see [artifact-digest-verify-ref][artifact-digest-verify-ref])
- LastUpdateTime: Timestamp of the last update of the artifact
- Size: Number of bytes in the file (decide beforehand on how to download the files)
- Metadata: Holds upstream information, e.g. OCI annotations (as map[string]string)

[`ArtifactStatus`][artifact-status]
- No fields

### Considered Options

* Option 1: Omit the `artifact`/`snapshot` concept
* Option 2: Use the `snapshot` implementation
* Option 3: Use the `artifact` implementation 
* Option 4: Use `OCIRepository` implementation from `FluxCD`
* Option 5: Create a new custom resource

### Decision Outcome

Chosen option: "Option 2: Use the `snapshot` implementation", because it is already implemented in the
`ocm-controllers` v1 and fits our use-cases most.

#### Positive Consequences

- Most of the functionality is already implemented, can be copied, and adjusted/refactored to our design.

#### Negative Consequences

- Requires a transformer that transforms the `snapshot` resource in something that, for example, FluxCDs
`source-controller` can consume. For this, the FluxCDs `OCIRepository` resource seems predestined.

### Pros and Cons of the Options

#### Option 1: Omit the `artifact`/`snapshot` concept

Instead of using an intermediate Custom Resource as `artifact` or `snapshot`, one could update the status of the source
resource that is creating the blob could point to the location of that blob itself.

Pros:
- No additional custom resource needed.

Cons:
- Since there is a "real" blob in the storage, it should have a respective entity to represent it, e.g.
`artifact`/`snapshot`

#### Option 2: Use the `snapshot` implementation

Pros:
- Already implemented (and probably tested).
- Implemented for an OCI registry

Cons:
- Require a transformer to make the artifacts consumable by FluxCDs Helm- and Kustomize-Controller. E.g. by using
FluxCDs `source-controller` and its CR `OCIRepository`.
- Implemented in `open-component-model/ocm-controller` which will be archived, when the `ocm-controller` v2 go
productive. Thus, the `snapshot` implementation must be copied in this repository.

#### Option 3: Use the `artifact` implementation

Pros:
- Already implemented (and a bit tested)
- Rather easy and simple

Cons:
- Implemented for a plain http-server and not for OCI registry (check 
[storage implementation][controller-manager-storage]). Thus, missing dedicated control-loop.
- Would require a custom deployment of controller of FluxCD

Basically rejected because we could only use the `Artifact` type definition and not the implementation for the storage.

#### Option 4: Use `OCIRepository` implementation from `FluxCD`

See [definition][oci-repository-type]. The type is part of the FluxCDs `source-controller`, which also
provides a control-loop for that resource.

Pros:
- No transformer needed for `FluxCD`s consumers Helm- and Kustomize-Controller 
- Control-loop for `OCIRepository` is already implemented
- `OCIRepository` is an integration point with Flux and Argo

Cons:
- Integrating FluxCDs `source-controller` would be a hard dependency on that repository. It would be mandatory
to deploy the `source-controller`
- It is not possible to start the `source-controller` and only watch the `OCIRepository` type. It would
start all other control-loops for `kustomize`, `helm`, `git`, and more objects. This seems a bit of an
overkill.
- Using the `OCIRepository` control-loop would basically "clone" every blob from the OCI registry in FluxCD
local storage (plain http server). 

Using `OCIRepository` as intermediate `storage`-pointer CR is not an option as the control-loop of that resource would
"clone" any OCI Registry blob to its own local storage.

#### Option 5: Create a new custom resource

Pros:
- Greenfield approach.
- Orientation on `snapshot` and `artifact` ease the implementation.

Cons:
- New implementation is required.

Creating a new custom resource seems like an overkill, considering that the `snapshot` implementation covers a lot of
our use-cases. Thus, it seems more reasonable to go with the `snapshot` implementation and adjust/refactor that.


## (Internal) OCI Registry

This in-cluster HTTPS-based registry is used by the OCM controllers to store resources locally. It should never be accessible from outside, thus it is transparent to the users of the OCM controller. At the same time the registry is accessible for Flux, running in the same cluster.


### Considered Options

* Option 1: Let the user provide a registry that is OCI compliant 
* Option 2: Deploy an OCI image registry with our controllers
  * Option 2.1: Use implementation from ocm-controllers v1
  * Option 2.2: Use [`zot`](https://github.com/project-zot/zot)

### Registry Selection Criteria

- Ease of deployment
- HTTPS support
- Garbage collection
-  ...

### Decision Outcome

Chosen option: "???", because... 

#### Positive Consequences

* …

#### Negative Consequences

* …

### Pros and Cons of the Options

### Option 1: Let the user provide a registry that is OCI compliant

Pros:
- Not our responsibility
- Users can customize their OCI registry like they want

Cons:
- We do not "control" the resource and issues caused by another OCI registry could be hard to fix/support
- Most people need to operate a registry than and the majority would not have experience maintaining a production grade stable oci registry as a service
- Giving a possibility to the user to provide/configure an own registry does not eliminate the need to provide a default registry (option 2), especially to those users who do not want to customize an own registry.

#### Option 2: Deploy an OCI image registry with our controllers

Pros:
- Simplifies deployment choices and stability guarantees for us.

Cons:
- ...

##### Option 2.1: Use implementation from ocm-controllers v1 ([distribution registry](https://github.com/distribution/distribution))

Pros:
- Faster implementation time, as deployment can be copied from v1 implementation
- Mature technology (almost legacy)

Cons:
- ...

###### Option 2.2: Use [`zot`](https://github.com/project-zot/zot)

Pros:
- Newer technology, aiming at minimal deployment, embedding into other products, inline garbage collection and storage deduplication
- Nice documentation
- FluxCD team mentioned (verbally) that they want to use a `zot` OCI registry in the future (though no 100% guarantee or any evidence that they started working on this so far)
- ...

Cons:
- Longer implementation time, as it involves learing how to deploy and operate a new registry
- ...

# Links
- Epic [#75](https://github.com/open-component-model/ocm-k8s-toolkit/issues/75)
- Issue [#90](https://github.com/open-component-model/ocm-k8s-toolkit/issues/90)

[artifact-definition]: https://github.com/openfluxcd/artifact/blob/d9db932260eb5f847737bcae3589b653398780ae/api/v1alpha1/artifact_types.go#L30
[fluxcd-rfc]: https://github.com/fluxcd/flux2/discussions/5058
[snapshot-definition]: https://github.com/open-component-model/ocm-controller/blob/8588071a05532abd28916931963f88b16622e44d/api/v1alpha1/snapshot_types.go#L22
[ocm-controller-v1-architecture]: https://github.com/open-component-model/ocm-controller/blob/8588071a05532abd28916931963f88b16622e44d/docs/architecture.md
[snapshot-spec]: https://github.com/open-component-model/ocm-controller/blob/8588071a05532abd28916931963f88b16622e44d/api/v1alpha1/snapshot_types.go#L22 
[snapshot-status]: https://github.com/open-component-model/ocm-controller/blob/8588071a05532abd28916931963f88b16622e44d/api/v1alpha1/snapshot_types.go#L35
[artifact-spec]: https://github.com/openfluxcd/artifact/blob/d9db932260eb5f847737bcae3589b653398780ae/api/v1alpha1/artifact_types.go#L30
[artifact-status]: https://github.com/openfluxcd/artifact/blob/d9db932260eb5f847737bcae3589b653398780ae/api/v1alpha1/artifact_types.go#L62
[go-containerregistry-digest]: https://github.com/google/go-containerregistry/blob/6bce25ecf0297c1aa9072bc665b5cf58d53e1c54/pkg/v1/manifest.go#L47
[snapshot-version-ref]: https://github.com/open-component-model/ocm-controller/blob/8588071a05532abd28916931963f88b16622e44d/controllers/resource_controller.go#L212
[snapshot-create-identity]: https://github.com/open-component-model/ocm-controller/blob/8588071a05532abd28916931963f88b16622e44d/controllers/resource_controller.go#L287
[artifact-digest-verify-ref]: https://github.com/openfluxcd/controller-manager/blob/d83030b764ab4f143d4b9a815227ad3cdfd9433f/storage/storage.go#L478
[oci-repository-type]: https://github.com/fluxcd/source-controller/blob/529eee0ed1afc6063acd9750aa598d90ae3399ed/api/v1beta2/ocirepository_types.go#L296
[controller-manager-storage]: https://github.com/openfluxcd/controller-manager/blob/d83030b764ab4f143d4b9a815227ad3cdfd9433f/storage/storage.go
[watch-resource-controller]: https://github.com/open-component-model/ocm-k8s-toolkit/blob/108ac97815258cef41cf8f340c99b45f7bdd5023/internal/controller/resource/resource_controller.go#L86
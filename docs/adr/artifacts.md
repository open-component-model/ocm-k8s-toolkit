# Use Single Layer OCI Artifacts for blobs

* Deciders: @frewilhelm @ikhandamirov

## Background

The controllers in this repository create artifacts/blobs that are used by one another. For example, the
component-controller creates an artifact containing the component descriptors from the specified component version.
Finally, the resource controller, or if specified the configuration controller, creates a blob as an artifact that holds
the resource that is consumed by the deployers.

Initially, it was planned to use a Custom Resource `artifact` type to represent these artifacts.
This `artifact` type was [defined][artifact-definition] to point to a URL and holds a "human-readable" identifier
`Revision` of a blob stored in a http-server inside the controller.

The `artifact` idea was part of a bigger [RFC][fluxcd-rfc] for `FluxCD`. Unfortunately, a possible implementation was
postponed to an unspecified time in the future and the implementation details were also unclear.
This was tantamount to a rejection.

Therefore, the original purpose of that Custom Resource `artifact` is not present anymore. Additionally, the team
decided to not use a plain http-server but an internal OCI registry to store and publish its blobs that are produced by
the OCM controllers as single layer OCI artifacts.

There are several options on how to proceed with the replacement and implementation that are discussed below.

## Artifact
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
  - Probably to check if the blob that the `snapshot` is pointing to is already equal to a newly created blob
- Tag: The version (e.g. `latest`, `v1.0.0`, ..., see [reference][snapshot-version-ref]
  - Purpose?
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
- Size: Number of bytes in the file
  - Purpose?
    - Maybe to check the size before downloading it? Maybe there are different option to download a bigger file?
- Metadata: Holds upstream information, e.g. OCI annotations (as map[string]string) (did we ever use that?) (Purpose?)

[`ArtifactStatus`][artifact-status]
- No fields

### Options

The following sections discusses several options.

#### Option 1: Omit the `artifact`/`snapshot` concept (Rejected)

Instead of using an intermediate Custom Resource as `artifact` or `snapshot`, one could update the status of the source
resource that is creating the blob could point to the location of that blob itself.

Pros:
- No additional custom resource needed.

Cons:
- Probably more complex to consume the blobs as the consumer must know the identity of the source resource and the
resulting blob.
- No selective watch for changes in the blob possible. The reconciliation would always trigger, when something on the
status of the source resources is changed. (Probably not true, since we watch the source resource that is referenced
by the consumer resource (see [watch of the resource controller][watch-resource-controller] ))
- Since there is a "real" blob in the storage, it should have a respective entity to represent it, e.g.
`artifact`/`snapshot`

#### Option 2: Use the `snapshot` implementation (accepted)

Pros:
- Already implemented (and probably tested).
- Implemented for an OCI registry

Cons:
- Status fields feel unnecessary.
- Require a transformer to make the artifacts consumable by `FluxCD`s Helm- and Kustomize-Controller (is probably
required either way).
- Implemented in `open-component-model/ocm-controller` which will be archived, when the `ocm-controller` v2 go
productive. Thus, the `snapshot` implementation must be copied in this repository.

#### Option 3: Use the `artifact` implementation (rejected)

Pros:
- Already implemented (and a bit tested)
- Rather easy and simple

Cons:
- In another GitHub organization that will be archived in the future as the initial purpose has vanished.
- Implemented for a plain http-server and not for OCI registry (check 
[storage implementation][controller-manager-storage]). Thus, missing dedicated control-loop.
- Requires an integration point for Flux and Argo.

Basically rejected because we could only use the `Artifact` type definition and not the implementation for the storage.

#### Option 4: Use `OCIRepository` implementation from `FluxCD` (rejected)

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

#### Option 5: Create a new custom resource (rejected)

Pros:
- Greenfield approach.
- Orientation on `snapshot` and `artifact` ease the implementation.

Cons:
- New implementation is required.

Creating a new custom resource seems like an overkill, considering that the `snapshot` implementation covers a lot of
our use-cases. Thus, it seems more reasonable to go with the `snapshot` implementation and adjust/refactor that.

### Conclusion

- Option 1: Omitting a dedicated resource for the storage objects is not a good design-choice.
- Option 2: The `snapshot` implementation fits our purposes and is already implemented.
- Option 3: The current `artifact` implementation is not feasible as it was written for another purpose.
- Option 4: The FluxCDs `OCIRepository` resource has major implications to our environment that are not beneficial.
- Option 5: Creating a new custom resource feels like an overkill since it requires a new implementation.

Therefore, option 2 is chosen as it is reasonable to use the already implemented `snapshot` resource and adjust it to
our purposes.

This approach requires a transformer to make the final resource consumable. For this, the FluxCDs `OCIRepository`
resource seems predestined, since `OCIRepository` can be consumed by FluxCDs `source-controller` (most use-cases) as
well as by ArgoCD.

## (Internal) OCI Registry
...

### Option 1: Let the user provide a registry that is OCI compliant

Pros:
- Not our responsibility
- Users can customize their OCI registry like they want
- ...

Cons:
- We do not "control" the resource and issues caused by another OCI registry could be hard to fix/support
- ...

### Option 2: Deploy an OCI image registry with our controllers

### Option 2.1: Use implementation from ocm-controllers v1

Pros:
- Already implemented.
- ...

Cons:
- ...

### Option 2.2: Use [`zot`](https://github.com/project-zot/zot)

Pros:
- Is the newest shot
- FluxCD mentions that they want to use a `zot` OCI registry in the future
- ...

Cons:
- Implement from scratch (probably not true)
- ...

## Links
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
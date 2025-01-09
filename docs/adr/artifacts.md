# Use Single Layer OCI Artifacts for blobs

* Deciders: @frewilhelm @ikhandamirov

## Background

Initially, it was planned to use a Custom Resource `Artifact` type to make blobs available between other controllers
as well as publishing the result to be consumed by anything that handles that `Artifact` type. 
This `Artifact` type was [defined][artifact-definition] to point to a URL and hold a human-readable identifier
`Revision` of a blob stored in a http-server inside the controller.

The `Artifact` idea was part of a bigger [RFC][fluxcd-rfc] for `FluxCD` which was unfortunately rejected.
Therefore, the Custom Resource `Artifact` is not needed anymore. Additionally, the team decided to not use a plain
http-server but an internal OCI registry to store and publish its blobs that are produced by the OCM controllers as 
single layer OCI artifacts.

There are several options on how to proceed with the replacement and implementation that are discussed below.

## Artifact
An artifact in the current context describes a resource that holds an identity to a blob and a pointer where to find
the blob (currently an URL). It that sense, a producer can create an artifact and store these information and a consumer
can search for artifacts with the specific identity to find out its location.

In the current implementation the artifact is defined in the [openfluxcd/artifact repository][artifact-definition].

The ocm-controller `v1` implementation defined a `snapshot` type that serves similar purposes.
Its definition can be found in [open-component-model/ocm-controller][snapshot-definition].


### Option 1: Use implementation from ocm-controllers v1

From [ocm-controller v1 Architecture][ocm-controller-v1-architecture]:
_snapshots are immutable, Flux-compatible, single layer OCI images containing a single OCM resource. 
Snapshots are stored in an in-cluster registry and in addition to making component resources accessible for 
transformation, they also can be used as a caching mechanism to reduce unnecessary calls to the source OCM registry._

Pros:
- ...

Cons:
- ...


### Option 2: Create Custom Resource `Artifact` (or the like) to point to the respective blob in the OCI registry
...

### Option 3: Remove Artifact concept (use status of other CRs instead)
Instead of using an intermediate resource, like `Artifact` or `Snapshot`, we could update the status from the source
resource with the location where to find its result.

For example, the `component` type status could hold the URL/tag to the blob holding the Component Descriptors that are
provided by the `component-controller`.

Pros:
- ...

Cons:
- ...


## (Internal) OCI Registry
...

### Option 1: Use implementation from ocm-controllers v1
...

### Option 2: Implement the deployment of an internal OCI registry
...

### Option 3: Use OCI registry that FluxCD will use in the future
...


### Option ...

## Links
- Epic [#75](https://github.com/open-component-model/ocm-k8s-toolkit/issues/75)
- Issue [#90](https://github.com/open-component-model/ocm-k8s-toolkit/issues/90)


[artifact-definition]: https://github.com/openfluxcd/artifact/blob/d9db932260eb5f847737bcae3589b653398780ae/api/v1alpha1/artifact_types.go#L30
[fluxcd-rfc]: https://github.com/fluxcd/flux2/discussions/5058
[snapshot-definition]: https://github.com/open-component-model/ocm-controller/blob/8588071a05532abd28916931963f88b16622e44d/api/v1alpha1/snapshot_types.go#L22
[ocm-controller-v1-architecture]: https://github.com/open-component-model/ocm-controller/blob/8588071a05532abd28916931963f88b16622e44d/docs/architecture.md
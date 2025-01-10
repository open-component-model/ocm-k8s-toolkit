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
- Revision: Human-readable identifier traceable in the origin source system (commit SHA, tag, version, ...)
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

#### Option 1: Omit the `artifact`/`snapshot` concept

Instead of using an intermediate custom resource as `artifact` or `snapshot`, one could update the status of the source
resource that is creating the blob could point to the location of that blob itself.

Pros:
- No additional custom resource needed.
- ...

Cons:
- Probably more complex to consume the blobs as the consumer must know the identity of the source resource and the
resulting blob.
- ...

#### Option 2: Use the `snapshot` implementation

Pros:
- Already implemented (and probably tested).
- Implemented for an OCI registry
- ...

Cons:
- Status fields feel unnecessary.
- ...

#### Option 3: Use the `artifact` implementation

Pros:
- Already implemented (and a bit tested)
- ...

Cons:
- In another org and repository (migration required)
- Implemented for a plain http-server
- ...

#### Option 4: Create a new Custom Resource based on `artifact` and `snapshot`

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
[artifact-digest-verify]: https://github.com/openfluxcd/controller-manager/blob/d83030b764ab4f143d4b9a815227ad3cdfd9433f/storage/storage.go#L478
# Replication

* Status: proposed
* Deciders: @fabianburth @jakobmoellerdev @frewilhelm @ikhandamirov 

Technical Story: 

The replication controller integrated into the ocm-k8s-toolkit mimics the `ocm transfer` behaviour into a controller. This allows transferring components from one registry to another  registry. A possible use case would be that the replication controller is running in a Management / Control-Plane cluster. One can subscribe to a certain component CR and each time the component version is updated in the CR, the newer version is automatically replicated to the target environment specified by an OCMRepository CR.

See also: ![use case](replication.png)

# Problem Statement and Proposed Solutions

## Problem 1: Will replication always only work on the latest or on all in spec (SemVer of Component status)?

Options:
1. Replicate all component versions fitting to semver specified in the Component CR Spec
2. Only replicate the latest version mentioned in the Component CR Status

Proposed solution:
* Option 2, i.e. replicate the version mentioned in the status of the component CR. 

Possible consequences:
* A possibility to replicate all versions or a range of versions can be added later, if required. Btw., OCM library currently does not provide this out of the box.
* Potential problem: it can happen that some in-between versions have not been replicated for some reason. How to get them replicated, if component CR is already on a greater version?

## Problem 2: Should a successful replication automatically result in creation of a component and resource CRs?

Options:
1. Yes, always create
2. Yes, if possible and desirable by the user
3. No

Proposed solution:
* No, in order to keep the scope of the initial controller in check. We can revisit this later if necessary. The assumption is that the component CR for the copy should be created on a different cluster, and that is out of the scope for the replication controller.

Possible consequences: ...

## Problem 3: How many replication attempts do you want to keep in replication CR status?

Options:
1. Unlimited
2. Hardcoded limit
3. CR-specific limit + default (if not specified in the CR)
4. Retain all not older then X days

Proposed solution:
* ..., because... 

Possible consequences: ...

## Problem 4: How to specify the transfer options?

Options:
1. As fields of the Replication CR
2. As ocmconfig of config type 'transport.ocm.config.ocm.software' stored in a separate k8s object

Note that:
* config type 'transport.ocm.config.ocm.software' would need to be extended to support more command line options. Though this extension looks logical, as the current differences between the config type and the command line options seem more like a mismatch at first glance.
* same question applies to uploader configuration
* `--lookup` seems to be a special case. A theoretical solution could be to introduce an array of source OCMRepository objects in the Replication CR.
* `--latest` is another special case. Do we need it, if component's status always provides a concrete version?
* not to forget `--disable-uploads` as there are know use cases for that.

Proposed solution:
* ..., because... 

Possible consequences: ...

## Problem 5: Should the Replication controller take the Artifact CR prepared by the Component Controller into account?

Options:
1. No, provide the OCM Lib with source coordinates and let it download the component version from scratch.
2. Use a custom source provider that allows the OCM Lib to fetch it from the HTTP URL. For this we might need a custom repository mapping that would expose it from a CTF.

Proposed solution:
* ..., because... 

Possible consequences: ...

## Problem 6: How to check the desired state of the Replication?

Options 1: Check the state of the component in the target registry

Decision driver: How do we know, if the state of the component in the target repository fits the current transfer options?

Option 2: Check the history (status) and compare it with the desired state in the Spec.

Option 3: Never check for desired state. Trigger the replication with every call to reconciler. Have no interval or large enough interval?

Proposed solution:
* ..., because... 

Possible consequences: ...

## Problem 7: Do we need to store ocmconfig with transfer options in a dedicated field in the Spec?

and a dedicated field in the Status with transfer options as clear text.

Options:
1. No, use generic ConfigRefs
2. Yes, use a dedicated field with a reference to a ConfigMap

Decision driver: an SRE might need a way to quickly find which transfer options an individual replication run was executed with.

Note: same question applies to uploader configuration.

Proposed solution:
* Use option 1 (generic ConfigRefs), because this is a common pattern across ocm-k8s-toolkit controllers.

Possible consequences: ...

## Problem 8: Should a Replication CR support several target OCMRepositories?

Options:
1. No, have one Replication CR per target repository
2. Yes, have one Replication CR to control replication to several target repositories.

Decision drivers: should be simple from ops / debugging PoV.

Proposed solution:
* Option 1, because it is easier to understand and to keep track of. We can always create a "ReplicationSet" later on that can deal with multiple targets. 

Possible consequences: ...

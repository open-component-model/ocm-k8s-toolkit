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

**Driver 1:** User Experience / Ease of Use / Ease of Adoption

The current localization controllers impose deep knowledge of the underlying replacement frameworks in OCM.
In the official [Example](https://ocm.software/docs/tutorials/structuring-and-deploying-software-products-with-ocm/#localization),
we propose a system that is based on file based replacements due to reliance on the [OCM Localization Tooling](https://github.com/open-component-model/ocm/blob/main/api/ocm/ocmutils/localize/README.md).

This tooling forces one to adopt:
1. Localization and Substitution Rules that need to be learned from scratch by platform engineers trying to adopt the tooling.
2. It is incredibly hard to understand based on these Rules which actual substitutions are being done, 
   as its a description framework with many layers of abstraction before the actual modification of the manifest:

For an example of this configuration process, see Option 3 (leaving the Status Quo in place).

This is a very complex way of describing a simple substitution rule for an image. Platform OPS are used
to a more direct way of describing the substitution rules via e.g. kustomize overlays ore helm values inserted directly
into the desired state of a deployable resource. Consider e.g. ArgoCDs Application Resource:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: sealed-secrets
  namespace: argocd
spec:
  project: default
  source:
    chart: sealed-secrets
    repoURL: https://bitnami-labs.github.io/sealed-secrets
    targetRevision: 1.16.1
    helm:
      releaseName: sealed-secrets
      valueFiles:
      - deploy-values.yaml
  destination:
    server: "https://kubernetes.default.svc"
    namespace: kubeseal
```

It is important to node that these tools can afford this because they rely on _existing_ templating capabilities of Helm or Kustomize.
Since we can operate on already rendered versions, we are free to template as we wish or introduce simple substitutions.

## Considered Options

**Option 1 - Pre-Processing / Templating**

Use a known templating system and / or language to introduce simple substitution rules into the component manifest resolution.

The easiest way to approach this problem can be to inject our own templating / pre-processing behavior into the component manifest resolution.
This would allow us to introduce simple substitution rules that are easy to understand and can be easily adopted by platform engineers.

Templating alone does introduce however new complexity in that we need to decide to either introduce 
our own templating system or adopt modification of existing templating systems in OCM core.

We can choose here to for example become opinionated and allow localized injection of Helm values, kustomize overlays
or go template values into the manifest after resolution.

Consider the following example:

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

This could be localized via the kustomize ImageTagTransformer (or any other transformer for that matter) with:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
images:
  - name: ghcr.io/open-component-model/mydeploymentinstruction
    newName: myprivateregistry.com/open-component-model/myimage
```

This implementation is pluggable in that it is based on kustomize Transformers and so we could add additional transformers where required.
Popular transforms for ConfigMaps and other substitutions exist.

We can then wrap this implementation in a reference such as:

```yaml
apiVersion: delivery.ocm.software/v1alpha1
kind: Localization
metadata:
  name: backend-localization
  namespace: ocm-system
spec:
  configRef:
    kind: ComponentVersion
    name: podinfocomponent-version
    namespace: ocm-system
    resourceRef:
      name: config
      referencePath:
        - name: backend
      version: 1.0.0
  interval: 10m0s
  sourceRef:
    kind: Kustomization
    # alternatively a kustomization file could be provided or a reference to a component version with the values inside.
    images:
      - name: ghcr.io/open-component-model/mydeploymentinstruction
        newName: myprivateregistry.com/open-component-model/myimage
```


**Option 2 - Literal Substitution Rules**

Generate literal substitution rules as manifest for the localization controller
based on templates embedded in the component.

If we allow component maintainers to define arbitrary placeholder literals in their component specification,
we can resolve them with a templating language of our choice (e.g. Go templates or spiff++ or any other).

This would allow us to generate a literal substitution rule manifest that can be used by the localization controller.

Consider the following example manifest:
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: mypod
spec:
  containers:
    - name: mypod
      image: {{ .localized.image }}
```

for a value based (simple) substitution, or the following (more complex example) for a Go Template based substitution:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: mypod
spec:
  containers:
    - name: mypod
      image: {{ .ResolveFromComponentVersion "ocm-system" "podinfocomponent-version" "1.0.0", "config" "backend" }}
```

This could be localized via a Go Template resolution by reading a values file:

```yaml
localized:
  image: myprivateregistry.com/open-component-model/myimage:v1.0.0
```

or by providing the given "ResolveFromComponentVersion" resolver function that is able to resolve the given 
component version and resource reference like the old localization controller did.

This can then be localized via a Go Template resolution:

```yaml
apiVersion: delivery.ocm.software/v1alpha1
kind: Localization
metadata:
  name: backend-localization
  namespace: ocm-system
spec:
  configRef:
    kind: ComponentVersion
    name: podinfocomponent-version
    namespace: ocm-system
    resourceRef:
      name: config
      referencePath:
        - name: backend
      version: 1.0.0
  interval: 10m0s
  sourceRef:
    kind: GoTemplate
    # alternatively a values file could be provided or a reference to a component version with the values inside.
    values:
      localized:
        image: myprivateregistry.com/open-component-model/myimage:v1.0.0
```

**Option 3 - Component-Based Subsitution Rules (previous solution)**

https://github.com/open-component-model/ocm-controller/blob/main/docs/architecture.md#localization-controller

Example:

```yaml
apiVersion: delivery.ocm.software/v1alpha1
kind: Localization
metadata:
  name: backend-localization
  namespace: ocm-system
spec:
  configRef:
    kind: ComponentVersion
    name: podinfocomponent-version
    namespace: ocm-system
    resourceRef:
      name: mydeploymentinstruction
      version: 1.0.0
  interval: 10m0s
  patchStrategicMerge: 
    source:
      sourceRef: 
        kind: GitRepository 
        name: gitRepo 
        namespace: default 
      path: "sites/eu-west-1/deployment.yaml" 
    target: 
      # Alternatively allow looking up via kind/name/namespace? This is not present currently, but could be implemented
      # kind: Deployment
      # name: deployment-in-mydeploymentinstruction
      # namespace: ocm-system
      path: "merge-target/merge-target.yaml"
```

**Option 4 - Substitution With Mutating Webhooks**

In this approach, any resources created by the Flux controllers will be redirected to a statically registered Webhook that manually
filters and mutates the resources before they are applied to the cluster. This is a very targeted approach mainly aiming
at image substitution for Pods, but technically other pod specifications may also be used.

For the example pod definition POST request to the API Server

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

The mutating webhook would then be able to substitute the image reference to the new location:

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
webhooks:
  - name: ocm.software/localization-mutating-webhook
    objectSelector:
      matchExpressions:
        - key: ocm.software/localization
          operator: NotIn
          values: ["disabled"]
        - key: owner
          operator: In
          values: ["ocm.software"]
    namespaceSelector:
      matchExpressions:
        - key: ocm.software/localization
          operator: NotIn
          values: ["disabled"]
    rules:
      - operations: ["CREATE","UPDATE"]
        apiGroups: ["*"]
        apiVersions: ["*"]
        resources: ["*"]
        scope: "Namespaced"
    sideEffects: None
    failurePolicy: Fail # Ignore ?
```

This will allow ocm to effectively get a request for every resource in the cluster that is controlled by it (in this case through an owner label, can be arbitrary)

In this instance one could configure a localization mapping as:

```yaml
apiVersion: delivery.ocm.software/v1alpha1
kind: Localization
metadata:
  name: backend-localization
  namespace: ocm-system
spec:
  replacements:
    images:
      - from: ghcr.io/open-component-model/mydeploymentinstruction
        to: myprivateregistry.com/open-component-model/myimage
        for:
          kind: Pod
          apiVersion: v1
# optionally added field paths for more specific replacements
#          fieldPaths: 
#          - spec.containers[0].image 
```


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


Advantages:

* Easy to understand and adopt for platform engineers
* Pluggable via kustomize transformers
* Can be used in conjunction with existing templating systems if injected into the resolution process
* Still allows default deployments without any localization
* Good, because the component is not invalid/inconsistent after the transport.

Disadvantages:

* Introduces a second templating step (that can be mitigated with resolution caching)
* Requires yet another templating step to be invoked (slower) in the resolution process
* Binds us to any used templating engine (be that kustomizes Transformers or not)
* Not easily possible to separate Localization from Configuration, meaning one Kustomization per Environment with non unified field specs.
* Bad, because the deployment instructions are not more valid manifest / helm
  charts / kustomize overlays.


### Option 2 - Literal Substitution Rules

Advantages:

* Allows a good mix between ease of use for simple substitution rules and complex substitution rules via resolution of functions or Go Templates.
* Is simple enough to be understood by any platform engineer with little exposure to go and cloud native technologies.
* Pluggable via custom functions that can be introduced to the Localization template parser.

Disadvantages:

* Requires a templating language to be introduced and one can no longer deploy the component version without Localization data present, except when defaults are programmed in.
* Binds us to a templating engine (Go Templates in this case)
* Not easily possible to separate Localization from Configuration, meaning one Go Template per Environment with non unified field specs. Alternatively the resolution mechanism
  would have to be able to resolve multiple values files.

### Option 3 - Component-Based Subsitution Rules (previous solution)

Advantages:

* Allows for a very fine grained control over the localization process
* Allows for separation of Localization and Configuration in a very fine grained level (2 separate CRDs)
* Allows to take over codebase from previous ocm controller
* Leverages existing replacement framework present in OCM with support for spiff++ and complex substitution rules

Disadvantages:

* New substitution / templating framework for most ops to learn
* Has issues with status reporting on actually replaced items in the Localization Custom Resource
* Requires us to write lots of code for a simple substitution rule
* Forces the concept of Localization separate from Configuration for all Scenarios

### Option 4 - Substitution With Mutating Webhooks

Advantages:

* Very easy to understand for all kubernetes experienced Ops
* Allows for fully decoupled localization process

Disadvantages:

* Requires Configuration of a Mutating Webhook in all clusters where the OCM controllers are deployed
* Requires the Mutating Webhook to be always online to avoid disruptions of resource creation/updates with Fail policy set.
* Will incur additional cost that scales with the amount of workloads and resources in the cluster, may prove disastrous in large clusters

## Links <!-- optional -->

* [Link type] [Link to ADR] <!-- example: Refined by [ADR-0005](0005-example.md) -->
* … <!-- numbers of links can vary -->

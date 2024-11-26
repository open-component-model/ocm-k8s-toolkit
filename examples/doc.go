/*
This package contains two sample components for testing purposes.

# ./podinfo-helm

Contains resources to deploy the Podinfo application using a Helm Chart. It
contains resources, localizations and configurations to further enhance and
test the controller's capabilities. The localization object uses an Image
resource to localize the helm chart by editing the image value in the values.yaml
file.

Further, it configures the following settings to custom values:
- ui.color
- ui.message

Once these are done, a `HelmRelease`, contained in `helm_release.yaml` file can
be used to install the modified helm chart. It should be observed that the frontend
has the configured color and displayed the configured message.

# ./podinfo-kustomize

This sample is essentially the same, however, it uses kustomize to deploy the
podinfo application. Within, is a simple Deployment (deployment.yaml) a Service
(service.yaml) HorizontalPodAutoscaler (hpa.yaml) and a kustomize object ( kustomization.yaml ).

The localization object changes the Deployment's `Image` property. The configuration
changes the pull image policy to `Always`.

Once the objects are successfully applied a Kustomize object can be created (flux_kustomization.yaml).

It must be observed that the podinfo application is deployed and after a little while,
a second pod application appears as the HPA will take effect.

Further information on creating can be viewed in the respective folders.
*/
package examples

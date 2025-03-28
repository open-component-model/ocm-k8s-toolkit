/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

// Ocm credential config key for secrets.
const (
	// OCMCredentialConfigKey defines the secret key to look for in case a user provides an ocm credential config.
	OCMCredentialConfigKey = ".ocmcredentialconfig" //nolint:gosec // G101 -- it isn't a credential
	// OCMConfigKey defines the secret or configmap key to look for in case a user provides an ocm config.
	OCMConfigKey = ".ocmconfig"
	// OCMLabelDowngradable defines the secret.
	OCMLabelDowngradable = "ocm.software/ocm-k8s-toolkit/downgradable"
	// OCMComponentDescriptorList defines the file name of the component descriptor list exposed as artifact by the
	// component controller.
	OCMComponentDescriptorList = "component-descriptor-list.yaml"
)

// Log levels.
const (
	// LevelDebug defines the depth at witch debug information is displayed.
	LevelDebug = 4
)

// Finalizers for controllers.
const (
	// ArtifactFinalizer is the finalizer that is added to artifacts created by the ocm controllers.
	ArtifactFinalizer = "finalizers.ocm.software/artifact"
)

// External CRDs.
const (
	ArtifactCrd = "https://github.com/openfluxcd/artifact/releases/download/v0.1.1/openfluxcd.ocm.software_artifacts.yaml"
)

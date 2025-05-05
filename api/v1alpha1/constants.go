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
)

// Log levels.
const (
	// LevelDebug defines the depth at witch debug information is displayed.
	LevelDebug = 4
)

// Finalizers for the controllers.
const (
	// ResourceFinalizer makes sure that the resource is only deleted when it is no longer referenced by any other
	// deployer.
	ResourceFinalizer = "finalizers.ocm.software/resource"
	// ComponentFinalizer makes sure that the component is only deleted when it is no longer referenced by any other
	// resource.
	ComponentFinalizer = "finalizers.ocm.software/component"
	// OCMRepositoryFinalizer makes sure that the OCM repository is only deleted when it is no longer referenced by any
	// other component.
	OCMRepositoryFinalizer = "finalizers.ocm.software/ocmrepository"
)

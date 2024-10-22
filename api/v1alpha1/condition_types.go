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

const (
	// SecretFetchFailedReason is used when the controller failed to fetch its secrets.
	SecretFetchFailedReason = "SecretFetchFailed"

	// ConfigFetchFailedReason is used when the controller failed to fetch its configs.
	ConfigFetchFailedReason = "ConfigFetchFailed"

	// VerificationsInvalidReason is used when the controller failed to gather the verification information.
	VerificationsInvalidReason = "VerificationsInvalid"

	// ConfigureContextFailedReason is used when the controller failed to create an authenticated context.
	ConfigureContextFailedReason = "ConfigureContextFailed"

	// CheckVersionFailedReason is used when the controller failed to check for new versions.
	CheckVersionFailedReason = "CheckVersionFailed"

	// RepositorySpecInvalidReason is used when the referenced repository spec cannot be unmarshaled and therefore is
	// invalid.
	RepositorySpecInvalidReason = "RepositorySpecInvalid"

	// RepositoryIsNotReadyReason is used when the referenced repository is not Ready yet.
	RepositoryIsNotReadyReason = "RepositoryIsNotReady"

	// ComponentIsNotReadyReason is used when the referenced component is not Ready yet.
	ComponentIsNotReadyReason = "ComponentIsNotReady"

	// ComponentIsNotReadyReason is used when the referenced component is not Ready yet.
	ReplicationFailedReason = "ReplicationFailed"

	// VerificationFailedReason is used when the signature verification of a component failed.
	VerificationFailedReason = "ComponentVerificationFailed"

	// GetComponentFailedReason is used when the component cannot be fetched.
	GetComponentFailedReason = "GetComponentFailed"

	// GetComponentDescriptorsFailedReason is used when the component descriptor cannot be fetched.
	GetComponentDescriptorsFailedReason = "GetComponentDescriptorsFailed"

	// GetComponentVersionFailedReason is used when the component cannot be fetched.
	GetComponentVersionFailedReason = "GetComponentVersionFailed"

	// StorageReconcileFailedReason is used when there was a problem reconciling the artifact storage.
	StorageReconcileFailedReason = "StorageReconcileFailed"

	// ReconcileArtifactFailedReason is used when we fail in creating an Artifact.
	ReconcileArtifactFailedReason = "ReconcileArtifactFailed"

	// GetArtifactFailedReason is used when we fail in getting an Artifact.
	GetArtifactFailedReason = "GetArtifactFailed"

	// ResolveResourceFailedReason is used when we fail in resolving a resource.
	ResolveResourceFailedReason = "ResolveResourceFailed"

	// GetResourceAccessFailedReason is used when we fail in getting a resource access(es).
	GetResourceAccessFailedReason = "GetResourceAccessFailed"

	// GetComponentForArtifactFailedReason is used when we fail in getting a component for an artifact.
	GetComponentForArtifactFailedReason = "GetComponentForArtifactFailed"

	// StatusSetFailedReason is used when we fail to set the component status.
	StatusSetFailedReason = "StatusSetFailed"
)

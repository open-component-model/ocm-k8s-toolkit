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
	// AuthenticatedContextCreationFailedReason is used when the controller failed to create an authenticated context.
	AuthenticatedContextCreationFailedReason = "AuthenticatedContextCreationFailed"

	// CheckVersionFailedReason is used when the controller failed to check for new versions.
	CheckVersionFailedReason = "CheckVersionFailed"

	// RepositoryIsNotReadyReason is used when the referenced repository is not Ready yet.
	RepositoryIsNotReadyReason = "RepositoryIsNotReady"

	// VerificationFailedReason is used when the signature verification of a component failed.
	VerificationFailedReason = "ComponentVerificationFailed"

	// GetComponentFailedReason is used when the component cannot be fetched.
	GetComponentFailedReason = "GetComponentFailed"

	// ComponentTraversalFailedReason is used when traversing any existing component references fails.
	ComponentTraversalFailedReason = "ComponentTraversalFailed"

	// StorageReconcileFailedReason is used when there was a problem reconciling the artifact storage.
	StorageReconcileFailedReason = "StorageReconcileFailed"

	// TemporaryFolderCreationFailedReason is used when creating a temporary folder fails.
	TemporaryFolderCreationFailedReason = "TemporaryFolderCreationFailed"

	// MarshallingComponentDescriptorsFailedReason is used when we can't serialize the component descriptor list.
	MarshallingComponentDescriptorsFailedReason = "MarshallingComponentDescriptorsFailed"

	// WritingComponentFileFailedReason is used when we fail to create the file for the component descriptors.
	WritingComponentFileFailedReason = "WritingComponentFileFailed"

	// ReconcileArtifactFailedReason is used when we fail in creating an Artifact.
	ReconcileArtifactFailedReason = "ReconcileArtifactFailed"
)

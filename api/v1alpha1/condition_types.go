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
	// ConfigFetchFailedReason is used when the controller failed to fetch its configs.
	ConfigFetchFailedReason = "ConfigFetchFailed"

	// ConfigureContextFailedReason is used when the controller failed to create an authenticated context.
	ConfigureContextFailedReason = "ConfigureContextFailed"

	// CheckVersionFailedReason is used when the controller failed to check for new versions.
	CheckVersionFailedReason = "CheckVersionFailed"

	// RepositorySpecInvalidReason is used when the referenced repository spec cannot be unmarshalled and therefore is
	// invalid.
	RepositorySpecInvalidReason = "RepositorySpecInvalid"

	// RepositoryIsNotReadyReason is used when the referenced repository is not Ready yet.
	RepositoryIsNotReadyReason = "RepositoryIsNotReady"

	// ComponentIsNotReadyReason is used when the referenced component is not Ready yet.
	ComponentIsNotReadyReason = "ComponentIsNotReady"

	// ResourceIsNotReadyReason is used when the referenced resource is not Ready yet.
	ResourceIsNotReadyReason = "ResourceIsNotReady"

	// ReplicationFailedReason is used when the referenced component is not Ready yet.
	ReplicationFailedReason = "ReplicationFailed"

	// VerificationFailedReason is used when the signature verification of a component failed.
	VerificationFailedReason = "ComponentVerificationFailed"

	// GetComponentFailedReason is used when the component cannot be fetched.
	GetComponentFailedReason = "GetComponentFailed"

	// GetComponentDescriptorsFailedReason is used when the component descriptor cannot be fetched.
	GetComponentDescriptorsFailedReason = "GetComponentDescriptorsFailed"

	// GetComponentVersionFailedReason is used when the component cannot be fetched.
	GetComponentVersionFailedReason = "GetComponentVersionFailed"

	// MarshalFailedReason is used when we fail to marshal a struct.
	MarshalFailedReason = "MarshalFailed"

	// CreateOrUpdateFailedReason is used when we fail to create or update a resource.
	CreateOrUpdateFailedReason = "CreateOrUpdateFailed"

	// YamlToJSONDecodeFailedReason is used when we fail to decode yaml to json.
	YamlToJSONDecodeFailedReason = "YamlToJsonDecodeFailed"

	// CreateOCIRepositoryNameFailedReason is used when we fail to create an OCI repository name.
	CreateOCIRepositoryNameFailedReason = "CreateOCIRepositoryNameFailed"

	// CreateOCIRepositoryFailedReason is used when we fail to create an OCI repository.
	CreateOCIRepositoryFailedReason = "CreateOCIRepositoryFailed"

	// PushOCIArtifactFailedReason is used when we fail to push an OCI artifact.
	PushOCIArtifactFailedReason = "PushOCIArtifactFailed"

	// FetchOCIArtifactFailedReason is used when we fail to fetch an OCI artifact.
	FetchOCIArtifactFailedReason = "FetchOCIArtifactFailed"

	// CopyOCIArtifactFailedReason is used when we fail to copy an OCI artifact.
	CopyOCIArtifactFailedReason = "CopyOCIArtifactFailed"

	// DeleteArtifactFailedReason is used when we fail to copy an OCI artifact.
	DeleteOCIArtifactFailedReason = "DeleteOCIArtifactFailed"

	// OCIRepositoryExistsFailedReason is used when we fail to check the existence of an OCI repository.
	OCIRepositoryExistsFailedReason = "OCIRepositoryExistsFailed"

	// ResolveResourceFailedReason is used when we fail in resolving a resource.
	ResolveResourceFailedReason = "ResolveResourceFailed"

	// GetResourceAccessFailedReason is used when we fail in getting a resource access(es).
	GetResourceAccessFailedReason = "GetResourceAccessFailed"

	// GetReferenceFailedReason is used when we fail to get a reference.
	GetReferenceFailedReason = "GetReferenceFailed"

	// GetBlobAccessFailedReason is used when we fail to get a blob access.
	GetBlobAccessFailedReason = "GetBlobAccessFailed"

	// VerifyResourceFailedReason is used when we fail to verify a resource.
	VerifyResourceFailedReason = "VerifyResourceFailed"

	// GetResourceFailedReason is used when we fail to get the resource.
	GetResourceFailedReason = "GetResourceFailed"

	// CompressGzipFailedReason is used when we fail to compress to gzip.
	CompressGzipFailedReason = "CompressGzipFailed"

	// StatusSetFailedReason is used when we fail to set the component status.
	StatusSetFailedReason = "StatusSetFailed"

	// TargetFetchFailedReason is used when a resource requiring a target to apply to cannot fetch this target.
	TargetFetchFailedReason = "TargetFetchFailed"

	// ConfigurationFailedReason is used when a resource was not able to be configured.
	ConfigurationFailedReason = "ConfigurationFailed"

	// CreateTGZFailedReason is used when a TGZ creation failed.
	CreateTGZFailedReason = "CreateTGZFailed"

	// LocalizationRuleGenerationFailedReason is used when the controller failed to localize an OCI artifact.
	LocalizationRuleGenerationFailedReason = "LocalizationRuleGenerationFailed"

	// LocalizationIsNotReadyReason is used when a controller is waiting to get the localization result.
	LocalizationIsNotReadyReason = "LocalizationIsNotReady"

	// UniqueIDGenerationFailedReason is used when the controller failed to generate a unique identifier for a pending OCI artifact.
	// This can happen if the OCI artifact is based on multiple other sources but these sources could not be used
	// to determine a unique identifier.
	UniqueIDGenerationFailedReason = "UniqueIDGenerationFailed"

	// ConfigGenerationFailedReason is used when the controller failed to generate a configuration
	// it needs to continue its reconciliation process.
	ConfigGenerationFailedReason = "ConfigGenerationFailed"

	// ResourceGenerationFailedReason is used when the controller failed to generate a resource
	// based on its specification.
	ResourceGenerationFailedReason = "ResourceGenerationFailed"
)

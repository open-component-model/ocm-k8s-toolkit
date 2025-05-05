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
	// ConfigureContextFailedReason is used when the controller failed to create an authenticated context.
	ConfigureContextFailedReason = "ConfigureContextFailed"

	// CheckVersionFailedReason is used when the controller failed to check for new versions.
	CheckVersionFailedReason = "CheckVersionFailed"

	// ResourceIsNotAvailable is used when the referenced resource is not available.
	ResourceIsNotAvailable = "ResourceIsNotAvailable"

	// ReplicationFailedReason is used when the referenced component is not Ready yet.
	ReplicationFailedReason = "ReplicationFailed"

	// GetOCMRepositoryFailedReason is used when the OCM repository cannot be fetched.
	GetOCMRepositoryFailedReason = "GetOCMRepositoryFailed"

	// GetComponentVersionFailedReason is used when the component cannot be fetched.
	GetComponentVersionFailedReason = "GetComponentVersionFailed"

	// GetOCMResourceFailedReason is used when the OCM resource cannot be fetched.
	GetOCMResourceFailedReason = "GetOCMResourceFailed"

	// MarshalFailedReason is used when we fail to marshal a struct.
	MarshalFailedReason = "MarshalFailed"

	// CreateOrUpdateFailedReason is used when we fail to create or update a resource.
	CreateOrUpdateFailedReason = "CreateOrUpdateFailed"

	// GetReferenceFailedReason is used when we fail to get a reference.
	GetReferenceFailedReason = "GetReferenceFailed"

	// GetResourceFailedReason is used when we fail to get the resource.
	GetResourceFailedReason = "GetResourceFailed"

	// StatusSetFailedReason is used when we fail to set the component status.
	StatusSetFailedReason = "StatusSetFailed"

	// DeletionFailedReason is used when we fail to delete the resource.
	DeletionFailedReason = "DeletionFailed"
)

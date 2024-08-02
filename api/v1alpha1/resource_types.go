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

import (
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ResourceSpec defines the desired state of Resource
type ResourceSpec struct {
	// +required
	RepositoryRef ObjectKey `json:"repositoryRef"`
	// +required
	ComponentRef ObjectKey `json:"componentRef"`
	// +required
	Resource ResourceId `json:"resource"`
	// +optional
	ResourceSelector ResourceSelector `json:"resourceSelector"`
	// +optional
	SecretRef v1.LocalObjectReference `json:"secretRef,omitempty"`
	// +optional
	SecretRefs []v1.LocalObjectReference `json:"secretRefs,omitempty"`
	// If the SecretRef (or SecretRefs) contain ocm config sets, the user may specify which config set he wants to be
	// effective.
	// +optional
	ConfigSet string `json:"configSet,omitempty"`
	// +required
	Interval metav1.Duration `json:"interval"`
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// ResourceStatus defines the observed state of Resource
type ResourceStatus struct {
	// +optional
	State string `json:"state,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// The component controller generates an artifact which is a list of component descriptors. If the components were
	// verified, other controllers (e.g. Resource controller) can use this without having to verify the signature again
	// +optional
	ArtifactRef v1.LocalObjectReference `json:"artifactRef,omitempty"`
	// +optional
	Artifact artifactv1.ArtifactSpec `json:"artifact,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	Component ComponentInfo `json:"component,omitempty"`
	// +optional
	Resource ResourceInfo `json:"resource,omitempty"`
	// Propagate its effective secrets. Other controllers (e.g. Resource controller) may use this as default
	// if they do not explicitly refer a secret.
	// +optional
	SecretRefs []v1.LocalObjectReference `json:"secretRefs,omitempty"`
	// +optional
	ConfigSet string `json:"configSet,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Resource is the Schema for the resources API
type Resource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ResourceSpec   `json:"spec,omitempty"`
	Status ResourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ResourceList contains a list of Resource
type ResourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Resource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Resource{}, &ResourceList{})
}

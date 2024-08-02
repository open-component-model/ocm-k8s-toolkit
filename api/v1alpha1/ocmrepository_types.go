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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// OCMRepositorySpec defines the desired state of OCMRepository
type OCMRepositorySpec struct {
	// +required
	RepositorySpec RepositorySpec `json:"repositorySpec"`
	// +optional
	SecretRef v1.LocalObjectReference `json:"secretRef,omitempty"`
	// +optional
	SecretRefs []v1.LocalObjectReference `json:"secretRefs,omitempty"`
	// If the SecretRef (or SecretRefs) contain ocm config sets, the user may specify which config set he wants to be
	// effective.
	// +optional
	ConfigSet string `json:"configSet"`
	// +required
	Interval metav1.Duration `json:"interval"`
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// OCMRepositoryStatus defines the observed state of OCMRepository
type OCMRepositoryStatus struct {
	// +optional
	State string `json:"state,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	RepositorySpec RepositorySpec `json:"repositorySpec,omitempty"`
	// Propagate its effective secrets. Other controllers (e.g. Component or Resource controller) may use this as default
	// if they do not explicitly refer a secret.
	// +optional
	SecretRefs []v1.LocalObjectReference `json:"secretRefs,omitempty"`
	// +optional
	ConfigSets string `json:"configSets,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// OCMRepository is the Schema for the ocmrepositories API
type OCMRepository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OCMRepositorySpec   `json:"spec,omitempty"`
	Status OCMRepositoryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OCMRepositoryList contains a list of OCMRepository
type OCMRepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OCMRepository `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OCMRepository{}, &OCMRepositoryList{})
}

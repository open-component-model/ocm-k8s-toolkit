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

// ComponentSpec defines the desired state of Component
type ComponentSpec struct {
	// +required
	RepositoryRef ObjectKey `json:"repositoryRef"`
	// +required
	Component string `json:"component"`
	// +required
	Semver string `json:"semver"`
	// +optional
	SemverFilter string `json:"semverFilter,omitempty"`
	// +optional
	Verify []Verification `json:"verify,omitempty"`
	// +optional
	SecretRef v1.LocalObjectReference `json:"secretRef,omitempty"`
	// +optional
	SecretRefs []v1.LocalObjectReference `json:"secretRefs,omitempty"`
	// The secrets referred to by SecretRef (or SecretRefs) may contain ocm config data. The ocm config allows to
	// specify sets of configuration data (s. https://ocm.software/docs/cli-reference/help/configfile/). If the
	// SecretRef (or SecretRefs) contain ocm config sets, the user may specify which config set he wants to be
	// effective.
	// +optional
	ConfigSet string `json:"configSet,omitempty"`
	// +required
	Interval metav1.Duration `json:"interval"`
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// ComponentStatus defines the observed state of Component
type ComponentStatus struct {
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
	// Propagate its effective secrets. Other controllers (e.g. Resource controller) may use this as default
	// if they do not explicitly refer a secret.
	// +optional
	SecretRefs []v1.LocalObjectReference `json:"secretRefs,omitempty"`
	// The secrets referred to by SecretRef (or SecretRefs) may contain ocm config data. The ocm config allows to
	// specify sets of configuration data (s. https://ocm.software/docs/cli-reference/help/configfile/). If the
	// SecretRef (or SecretRefs) contain ocm config sets, the user may specify which config set he wants to be
	// effective.
	// +optional
	ConfigSet string `json:"configSet,omitempty"`
	// +optional
	ArtifactName string `json:"artifactName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Component is the Schema for the components API
type Component struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ComponentSpec   `json:"spec,omitempty"`
	Status ComponentStatus `json:"status,omitempty"`
}

func (in *Component) GetObjectMeta() *metav1.ObjectMeta {
	return &in.ObjectMeta
}

func (in *Component) GetKind() string {
	return "Component"
}

// +kubebuilder:object:root=true

// ComponentList contains a list of Component
type ComponentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Component `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Component{}, &ComponentList{})
}

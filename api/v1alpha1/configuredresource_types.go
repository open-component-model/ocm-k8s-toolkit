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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ConfiguredResourceSpec defines the desired state of ConfiguredResource.
type ConfiguredResourceSpec struct {
	// Target that is to be configured.
	// +required
	Target ConfigurationReference `json:"target,omitempty"`

	// Config that is to be used to configure the target
	// +required
	Config ConfigurationReference `json:"config,omitempty"`

	// Source of the configuration (where to get values from)
	// +required
	Source ConfigurationReference `json:"source,omitempty"`

	// Interval at which to refresh the configuration
	// +required
	Interval metav1.Duration `json:"interval"`

	// Suspend all localization behaviors, but keep existing localizations in place
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

func (in *ConfiguredResource) GetObjectMeta() *metav1.ObjectMeta {
	return &in.ObjectMeta
}

func (in *ConfiguredResource) GetKind() string {
	return "ConfiguredResource"
}

func (in *ConfiguredResource) SetObservedGeneration(v int64) {
	in.Status.ObservedGeneration = v
}

func (in *ConfiguredResource) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

func (in *ConfiguredResource) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

func (in *ConfiguredResource) GetVID() map[string]string {
	vid := fmt.Sprintf("%s:%s", in.Namespace, in.Name)
	metadata := make(map[string]string)
	metadata[GroupVersion.Group+"/localization"] = vid

	return metadata
}

// ConfiguredResourceStatus defines the observed state of ConfiguredResource.
type ConfiguredResourceStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// The component controller generates an artifact which is a list of component descriptors. If the components were
	// verified, other controllers (e.g. Resource controller) can use this without having to verify the signature again
	// +optional
	ArtifactRef *ObjectKey `json:"artifactRef,omitempty"`

	Digest string `json:"digest,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ConfiguredResource is the Schema for the configuredresources API.
type ConfiguredResource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConfiguredResourceSpec   `json:"spec,omitempty"`
	Status ConfiguredResourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConfiguredResourceList contains a list of ConfiguredResource.
type ConfiguredResourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConfiguredResource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConfiguredResource{}, &ConfiguredResourceList{})
}

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
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const KindResource = "Resource"

// ResourceSpec defines the desired state of Resource.
type ResourceSpec struct {
	// ComponentRef is a reference to a Component.
	// +required
	ComponentRef corev1.LocalObjectReference `json:"componentRef"`

	// Resource identifies the ocm resource to be fetched.
	// +required
	Resource ResourceID `json:"resource"`

	// OCMConfig defines references to secrets, config maps or ocm api
	// objects providing configuration data including credentials.
	// +optional
	OCMConfig []OCMConfiguration `json:"ocmConfig,omitempty"`

	// Interval at which the resource is checked for updates.
	// +required
	Interval metav1.Duration `json:"interval"`

	// Suspend tells the controller to suspend the reconciliation of this
	// Resource.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// ResourceStatus defines the observed state of Resource.
type ResourceStatus struct {
	// ObservedGeneration is the last observed generation of the Resource
	// object.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions holds the conditions for the Resource.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ArtifactRef points to the Artifact which represents the output of the
	// last successful Resource sync.
	// +optional
	ArtifactRef corev1.LocalObjectReference `json:"artifactRef,omitempty"`

	// +optional
	Resource *ResourceInfo `json:"resource,omitempty"`

	// EffectiveOCMConfig specifies the entirety of config maps and secrets
	// whose configuration data was applied to the Resource reconciliation,
	// in the order the configuration data was applied.
	// +optional
	EffectiveOCMConfig []OCMConfiguration `json:"effectiveOCMConfig,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Resource is the Schema for the resources API.
type Resource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ResourceSpec   `json:"spec,omitempty"`
	Status ResourceStatus `json:"status,omitempty"`
}

func (in *Resource) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

func (in *Resource) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

func (in *Resource) GetVID() map[string]string {
	vid := fmt.Sprintf("%s:%s", in.GetNamespace(), in.GetName())
	metadata := make(map[string]string)
	metadata[GroupVersion.Group+"/resource_version"] = vid

	return metadata
}

func (in *Resource) SetObservedGeneration(v int64) {
	in.Status.ObservedGeneration = v
}

func (in *Resource) GetObjectMeta() *metav1.ObjectMeta {
	return &in.ObjectMeta
}

func (in *Resource) GetKind() string {
	return KindResource
}

// GetRequeueAfter returns the duration after which the Resource must be
// reconciled again.
func (in Resource) GetRequeueAfter() time.Duration {
	return in.Spec.Interval.Duration
}
func (in *Resource) GetSpecifiedOCMConfig() []OCMConfiguration {
	return slices.Clone(in.Spec.OCMConfig)
}

func (in *Resource) GetPropagatedOCMConfig() []OCMConfiguration {
	var propagatedConfigs []OCMConfiguration
	for _, ocmconfig := range in.Status.EffectiveOCMConfig {
		if ocmconfig.Policy == ConfigurationPolicyPropagate {
			propagatedConfigs = append(propagatedConfigs, ocmconfig)
		}
	}

	return propagatedConfigs
}

// +kubebuilder:object:root=true

// ResourceList contains a list of Resource.
type ResourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Resource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Resource{}, &ResourceList{})
}

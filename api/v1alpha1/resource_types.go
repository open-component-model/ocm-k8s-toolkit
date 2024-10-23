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
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourceSpec defines the desired state of Resource.
type ResourceSpec struct {
	// ComponentRef is a reference to a Component.
	// +required
	ComponentRef v1.LocalObjectReference `json:"componentRef"`

	// Resource identifies the ocm resource to be fetched.
	// +required
	Resource ResourceID `json:"resource"`

	// +optional
	SecretRefs []v1.LocalObjectReference `json:"secretRefs,omitempty"`

	// +optional
	ConfigRefs []v1.LocalObjectReference `json:"configRefs,omitempty"`

	// The secrets and configs referred to by SecretRef (or SecretRefs) and Config (or ConfigRefs) may contain ocm
	// config data. The  ocm config allows to specify sets of configuration data
	// (s. https://ocm.software/docs/cli-reference/help/configfile/). If the SecretRef (or SecretRefs) and ConfigRef and
	// ConfigRefs contain ocm config sets, the user may specify which config set he wants to be effective.
	// +optional
	ConfigSet *string `json:"configSet,omitempty"`

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
	ArtifactRef v1.LocalObjectReference `json:"artifactRef,omitempty"`

	// +optional
	Resource *ResourceInfo `json:"resource,omitempty"`

	// Propagate its effective secrets. Other controllers (e.g. Resource
	// controller) may use this as default if they do not explicitly refer a
	// secret.
	// This is required to allow transitive defaulting (thus, e.g. Component
	// defaults from OCMRepository and Resource defaults from Component) without
	// having to traverse the entire chain.
	// +optional
	SecretRefs []v1.LocalObjectReference `json:"secretRefs,omitempty"`

	// Propagate its effective configs. Other controllers (e.g. Component or
	// Resource controller) may use this as default if they do not explicitly
	// refer a config.
	// This is required to allow transitive defaulting (thus, e.g. Component
	// defaults from OCMRepository and Resource defaults from Component) without
	// having to traverse the entire chain.
	// +optional
	ConfigRefs []v1.LocalObjectReference `json:"configRefs,omitempty"`

	// Propagate its effective config set. Other controllers (e.g. Component or
	// Resource controller) may use this as default if they do not explicitly
	// specify a config set.
	// This is required to allow transitive defaulting (thus, e.g. Component
	// defaults from OCMRepository and Resource defaults from Component) without
	// having to traverse the entire chain.
	// +optional
	ConfigSet string `json:"configSet,omitempty"`
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
	return "Resource"
}

// GetRequeueAfter returns the duration after which the Resource must be
// reconciled again.
func (in Resource) GetRequeueAfter() time.Duration {
	return in.Spec.Interval.Duration
}

func (in *Resource) GetSecretRefs() []v1.LocalObjectReference {
	return in.Spec.SecretRefs
}

func (in *Resource) GetEffectiveSecretRefs() []v1.LocalObjectReference {
	return in.Status.SecretRefs
}

func (in *Resource) GetConfigRefs() []v1.LocalObjectReference {
	return in.Spec.ConfigRefs
}

func (in *Resource) GetEffectiveConfigRefs() []v1.LocalObjectReference {
	return in.Status.ConfigRefs
}

func (in *Resource) GetConfigSet() *string {
	return in.Spec.ConfigSet
}

func (in *Resource) GetEffectiveConfigSet() string {
	return in.Status.ConfigSet
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

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
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OCMRepositorySpec defines the desired state of OCMRepository.
type OCMRepositorySpec struct {
	// RepositorySpec is the config of an ocm repository containing component
	// versions. This config has to be a valid ocm repository implementation
	// specification
	// https://github.com/open-component-model/ocm-spec/blob/main/doc/04-extensions/03-storage-backends/README.md.
	// +required
	RepositorySpec *apiextensionsv1.JSON `json:"repositorySpec"`

	// SecretRefs are references to one or multiple secrets that contain
	// credentials or ocm configurations
	// (https://github.com/open-component-model/ocm/blob/main/docs/reference/ocm_configfile.md).
	// +optional
	SecretRefs []v1.LocalObjectReference `json:"secretRefs,omitempty"`

	// ConfigRefs are references to one or multiple config maps that contain
	// ocm configurations
	// (https://github.com/open-component-model/ocm/blob/main/docs/reference/ocm_configfile.md).
	// +optional
	ConfigRefs []v1.LocalObjectReference `json:"configRefs,omitempty"`

	// The secrets and configs referred to by SecretRef (or SecretRefs) and
	// Config (or ConfigRefs) may contain ocm config data. The  ocm config
	// allows to specify sets of configuration data
	// (s. https://ocm.software/docs/cli-reference/help/configfile/). If the
	// SecretRef (or SecretRefs) and ConfigRef and ConfigRefs contain ocm config
	// sets, the user may specify which config set he wants to be effective.
	// +optional
	ConfigSet *string `json:"configSet"`

	// Interval at which the ocm repository specified by the RepositorySpec
	// validated.
	// +required
	Interval metav1.Duration `json:"interval"`

	// Suspend tells the controller to suspend the reconciliation of this
	// OCMRepository.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// OCMRepositoryStatus defines the observed state of OCMRepository.
type OCMRepositoryStatus struct {
	// ObservedGeneration is the last observed generation of the OCMRepository
	// object.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions holds the conditions for the OCMRepository.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Propagate its effective secrets. Other controllers (e.g. Component or
	// Resource controller) may use this as default if they do not explicitly
	// refer a secret.
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

// GetConditions returns the conditions of the OCMRepository.
func (in *OCMRepository) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the conditions of the OCMRepository.
func (in *OCMRepository) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

// GetRequeueAfter returns the duration after which the ComponentVersion must be
// reconciled again.
func (in OCMRepository) GetRequeueAfter() time.Duration {
	return in.Spec.Interval.Duration
}

// GetVID unique identifier of the object.
func (in *OCMRepository) GetVID() map[string]string {
	vid := fmt.Sprintf("%s:%s", in.Namespace, in.Name)
	metadata := make(map[string]string)
	metadata[GroupVersion.Group+"/repository"] = vid

	return metadata
}

func (in *OCMRepository) SetObservedGeneration(v int64) {
	in.Status.ObservedGeneration = v
}

func (in *OCMRepository) GetSecretRefs() []v1.LocalObjectReference {
	return in.Spec.SecretRefs
}

func (in *OCMRepository) SetEffectiveSecretRefs() {
	in.Status.SecretRefs = slices.Clone(in.GetSecretRefs())
}

func (in *OCMRepository) GetEffectiveSecretRefs() []v1.LocalObjectReference {
	return in.Status.SecretRefs
}

func (in *OCMRepository) GetConfigRefs() []v1.LocalObjectReference {
	return in.Spec.ConfigRefs
}

func (in *OCMRepository) SetEffectiveConfigRefs() {
	in.Status.ConfigRefs = slices.Clone(in.GetConfigRefs())
}

func (in *OCMRepository) GetEffectiveConfigRefs() []v1.LocalObjectReference {
	return in.Status.ConfigRefs
}

func (in *OCMRepository) GetConfigSet() *string {
	return in.Spec.ConfigSet
}

func (in *OCMRepository) SetEffectiveConfigSet() {
	if in.Spec.ConfigSet != nil {
		in.Status.ConfigSet = strings.Clone(*in.Spec.ConfigSet)
	}
}

func (in *OCMRepository) GetEffectiveConfigSet() string {
	return in.Status.ConfigSet
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// OCMRepository is the Schema for the ocmrepositories API.
type OCMRepository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OCMRepositorySpec   `json:"spec"`
	Status OCMRepositoryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OCMRepositoryList contains a list of OCMRepository.
type OCMRepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OCMRepository `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OCMRepository{}, &OCMRepositoryList{})
}

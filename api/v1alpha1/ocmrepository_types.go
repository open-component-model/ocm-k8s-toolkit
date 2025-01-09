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

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const KindOCMRepository = "OCMRepository"

// OCMRepositorySpec defines the desired state of OCMRepository.
type OCMRepositorySpec struct {
	// RepositorySpec is the config of an ocm repository containing component
	// versions. This config has to be a valid ocm repository implementation
	// specification
	// https://github.com/open-component-model/ocm-spec/blob/main/doc/04-extensions/03-storage-backends/README.md.
	// +required
	RepositorySpec *apiextensionsv1.JSON `json:"repositorySpec"`

	// OCMConfig defines references to secrets, config maps or ocm api
	// objects providing configuration data including credentials.
	// +optional
	OCMConfig []OCMConfiguration `json:"ocmConfig,omitempty"`

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

	// EffectiveOCMConfig specifies the entirety of config maps and secrets
	// whose configuration data was applied to the OCMRepository reconciliation,
	// in the order the configuration data was applied.
	// +optional
	EffectiveOCMConfig []OCMConfiguration `json:"effectiveOCMConfig,omitempty"`
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

func (in *OCMRepository) GetSpecifiedOCMConfig() []OCMConfiguration {
	return slices.Clone(in.Spec.OCMConfig)
}

func (in *OCMRepository) GetPropagatedOCMConfig() []OCMConfiguration {
	var propagatedConfigs []OCMConfiguration
	for _, ocmconfig := range in.Status.EffectiveOCMConfig {
		if ocmconfig.Policy == ConfigurationPolicyPropagate {
			propagatedConfigs = append(propagatedConfigs, ocmconfig)
		}
	}

	return propagatedConfigs
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

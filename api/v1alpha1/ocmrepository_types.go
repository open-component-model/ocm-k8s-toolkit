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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OCMRepositorySpec defines the desired state of OCMRepository.
type OCMRepositorySpec struct {
	// RepositorySpec is the config of the repository containing the component version.
	// Used by RepositoryForConfig to initialize the needed
	// +required
	RepositorySpec *apiextensionsv1.JSON `json:"repositorySpec"`
	// +optional
	SecretRef v1.LocalObjectReference `json:"secretRef,omitempty"`
	// +optional
	SecretRefs []v1.LocalObjectReference `json:"secretRefs,omitempty"`
	// The secrets referred to by SecretRef (or SecretRefs) may contain ocm config data. The ocm config allows to
	// specify sets of configuration data (s. https://ocm.software/docs/cli-reference/help/configfile/). If the
	// SecretRef (or SecretRefs) contain ocm config sets, the user may specify which config set he wants to be
	// effective.
	// +optional
	ConfigSet string `json:"configSet"`
	// +required
	Interval metav1.Duration `json:"interval"`
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// OCMRepositoryStatus defines the observed state of OCMRepository.
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
	RepositorySpec *apiextensionsv1.JSON `json:"repositorySpec,omitempty"`
	// Propagate its effective secrets. Other controllers (e.g. Component or Resource controller) may use this as default
	// if they do not explicitly refer a secret.
	// +optional
	SecretRefs []v1.LocalObjectReference `json:"secretRefs,omitempty"`
	// The secrets referred to by SecretRef (or SecretRefs) may contain ocm config data. The ocm config allows to
	// specify sets of configuration data (s. https://ocm.software/docs/cli-reference/help/configfile/). If the
	// SecretRef (or SecretRefs) contain ocm config sets, the user may specify which config set he wants to be
	// effective.
	// +optional
	ConfigSets string `json:"configSets,omitempty"`
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

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// OCMRepository is the Schema for the ocmrepositories API.
type OCMRepository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OCMRepositorySpec   `json:"spec,omitempty"`
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

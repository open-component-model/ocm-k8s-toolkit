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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The Replication essentially maps the ocm transfer behavior into a controller
// (exposing a subset of its options in the manifest).
// This allows transferring components into a private registry based on a "ocmops" based process.

// ReplicationSpec defines the desired state of Replication.
type ReplicationSpec struct {
	// ComponentRef is a reference to a Component to be replicated.
	// +required
	ComponentRef ObjectKey `json:"componentRef"`

	// targetRepositoryRef is a reference to an OCMRepository the component to be replicated to.
	// +required
	TargetRepositoryRef ObjectKey `json:"targetRepositoryRef"`

	// Interval at which the replication is reconciled.
	// +required
	Interval metav1.Duration `json:"interval"`

	// Suspend tells the controller to suspend the reconciliation of this
	// Replication.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// ReplicationStatus defines the observed state of Replication.
type ReplicationStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Replication is the Schema for the replications API.
type Replication struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReplicationSpec   `json:"spec,omitempty"`
	Status ReplicationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ReplicationList contains a list of Replication.
type ReplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Replication `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Replication{}, &ReplicationList{})
}

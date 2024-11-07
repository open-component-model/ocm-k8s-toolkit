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

	// HistoryCapacity is the maximum number of last replication runs to keep information about.
	// +kubebuilder:default:=10
	// +optional
	HistoryCapacity int `json:"historyLength,omitempty"`
}

// ReplicationStatus defines the observed state of Replication.
type ReplicationStatus struct {
	// History holds the history of replication runs.
	// +optional
	History []TransferRun `json:"history,omitempty"`

	// ObservedGeneration is the last observed generation of the Replication
	// object.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions holds the conditions for the Replication.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// TransferRun holds the status of a single 'ocm transfer' run.
type TransferRun struct {
	// Component is the fully qualified name of the OCM component.
	// +required
	Component string `json:"component"`

	// Version is the version of the component which was required to be replicated
	// +required
	Version string `json:"version"`

	// SourceRepositorySpec is the specification of the source repository.
	// +required
	SourceRepositorySpec string `json:"sourceRepositorySpec"`

	// TargetRepositorySpec is the specification of the target repository.
	// +required
	TargetRepositorySpec string `json:"targetRepositorySpec"`

	// StartTime is the time at which the replication run started.
	// +required
	StartTime metav1.Time `json:"startTime"`

	// EndTime is the time at which the replication run ended.
	// +optional
	EndTime metav1.Time `json:"endTime,omitempty"`

	// Error is the error message if the replication run failed.
	// +optional
	Error string `json:"error,omitempty"`

	// Success indicates whether the replication run was successful.
	// +required
	Success bool `json:"success"`
}

// GetConditions returns the conditions of the OCMRepository.
func (repl *Replication) GetConditions() []metav1.Condition {
	return repl.Status.Conditions
}

// SetConditions sets the conditions of the OCMRepository.
func (repl *Replication) SetConditions(conditions []metav1.Condition) {
	repl.Status.Conditions = conditions
}

// GetRequeueAfter returns the duration after which the ComponentVersion must be
// reconciled again.
func (repl Replication) GetRequeueAfter() time.Duration {
	return repl.Spec.Interval.Duration
}

// GetVID unique identifier of the object.
func (repl *Replication) GetVID() map[string]string {
	vid := fmt.Sprintf("%s:%s", repl.Namespace, repl.Name)
	metadata := make(map[string]string)
	metadata[GroupVersion.Group+"/replication"] = vid

	return metadata
}

func (repl *Replication) SetObservedGeneration(v int64) {
	repl.Status.ObservedGeneration = v
}

func (repl *Replication) AddHistoryRecord(rec TransferRun) {
	if len(repl.Status.History) >= repl.Spec.HistoryCapacity {
		repl.Status.History = repl.Status.History[1:]
	}
	repl.Status.History = append(repl.Status.History, rec)
}

func (repl *Replication) IsInHistory(component, version, targetSpec string) bool {
	for _, record := range repl.Status.History {
		if record.Component == component &&
			record.Version == version &&
			record.TargetRepositorySpec == targetSpec &&
			record.Success {
			return true
		}
	}

	return false
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

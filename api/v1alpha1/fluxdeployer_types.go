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

	"github.com/fluxcd/pkg/apis/meta"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FluxDeployerSpec defines the desired state of FluxDeployer.
type FluxDeployerSpec struct {
	// +required
	SourceRef meta.NamespacedObjectKindReference `json:"sourceRef"`

	// The interval at which to reconcile the Kustomization and Helm Releases.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$"
	// +required
	Interval metav1.Duration `json:"interval"`

	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	// +optional
	KustomizationTemplate *kustomizev1.KustomizationSpec `json:"kustomizationTemplate,omitempty"`

	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	// +optional
	HelmReleaseTemplate *helmv2.HelmReleaseSpec `json:"helmReleaseTemplate,omitempty"`

	// WaitForReady if set will wait for all created resources to be ready before itself becomes Ready.
	// +optional
	WaitForReady bool `json:"waitForReady,omitempty"`
}

// FluxDeployerStatus defines the observed state of FluxDeployer.
type FluxDeployerStatus struct {
	// ObservedGeneration is the last reconciled generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	Kustomization string `json:"kustomization"`

	// +optional
	OCIRepository string `json:"ociRepository"`

	// +optional
	HelmRelease string `json:"helmRelease"`
}

// GetConditions returns the conditions of the ComponentVersion.
func (in *FluxDeployer) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the conditions of the ComponentVersion.
func (in *FluxDeployer) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

func (in *FluxDeployer) GetRequeueAfter() time.Duration {
	return in.Spec.Interval.Duration
}

func (in *FluxDeployer) GetVID() map[string]string {
	vid := fmt.Sprintf("%s:%s", in.Namespace, in.Name)
	metadata := make(map[string]string)
	metadata[GroupVersion.Group+"/flux_deployer"] = vid

	return metadata
}

func (in *FluxDeployer) SetObservedGeneration(v int64) {
	in.Status.ObservedGeneration = v
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=fd
//+kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description=""
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description=""

// FluxDeployer is the Schema for the FluxDeployers API.
type FluxDeployer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FluxDeployerSpec   `json:"spec,omitempty"`
	Status FluxDeployerStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// FluxDeployerList contains a list of FluxDeployer.
type FluxDeployerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FluxDeployer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FluxDeployer{}, &FluxDeployerList{})
}

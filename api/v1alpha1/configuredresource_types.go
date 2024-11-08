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

	"github.com/fluxcd/pkg/apis/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const KindConfiguredResource = "ConfiguredResource"

// ConfiguredResourceSpec defines the desired state of ConfiguredResource.
type ConfiguredResourceSpec struct {
	// Target that is to be configured.
	Target ConfigurationReference `json:"target"`

	// Config that is to be used to configure the target
	Config ConfigurationReference `json:"config"`

	// Interval at which to refresh the configuration
	Interval metav1.Duration `json:"interval"`

	// Suspend all localization behaviors, but keep existing localizations in place
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

func (in *ConfiguredResource) GetConfig() *ConfigurationReference {
	return &in.Spec.Config
}

func (in *ConfiguredResource) GetTarget() *ConfigurationReference {
	return &in.Spec.Target
}

func (in *ConfiguredResource) SetConfig(v *ConfigurationReference) {
	v.DeepCopyInto(&in.Spec.Config)
}

func (in *ConfiguredResource) SetTarget(v *ConfigurationReference) {
	v.DeepCopyInto(&in.Spec.Target)
}

// ConfiguredResourceStatus defines the observed state of ConfiguredResource.
type ConfiguredResourceStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`

	// The configuration reconcile loop generates an artifact, which contains the
	// ConfiguredResourceSpec.Target ConfigurationReference after configuration.
	// It is filled once the Artifact is created and the configuration completed.
	ArtifactRef *ObjectKey `json:"artifactRef,omitempty"`

	// Digest contains a technical identifier for the artifact. This technical identifier
	// can be used to track changes on the ArtifactRef as it is a combination of the origin
	// ConfiguredResourceSpec.Config applied to the ConfiguredResourceSpec.Target.
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

// ConfigurationReference defines a configuration which may be accessed through an object in the cluster
// +kubebuilder:validation:MinProperties=1
type ConfigurationReference struct {
	meta.NamespacedObjectKindReference `json:",inline"`
}

func ResourceToConfigurationReference(r *Resource) ConfigurationReference {
	return ToConfigurationReference(r, KindResource)
}

func ResourceConfigToConfigurationReference(r *ResourceConfig) ConfigurationReference {
	return ToConfigurationReference(r, KindResourceConfig)
}

func ToConfigurationReference(obj metav1.Object, kind string) ConfigurationReference {
	apiVersion, kind := GroupVersion.WithKind(kind).ToAPIVersionAndKind()

	return ConfigurationReference{
		NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
			APIVersion: apiVersion,
			Kind:       kind,
			Name:       obj.GetName(),
			Namespace:  obj.GetNamespace(),
		},
	}
}

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

	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ComponentSpec defines the desired state of Component.
type ComponentSpec struct {
	// +required
	RepositoryRef ObjectKey `json:"repositoryRef"`
	// +required
	Component string `json:"component"`
	// Semver defines the constraint of the fetched version. '>=v0.1'.
	// +required
	Semver string `json:"semver"`
	// +optional
	SemverFilter string `json:"semverFilter,omitempty"`
	// +optional
	Verify []Verification `json:"verify,omitempty"`
	// TODO: What's the purpose of this compared to the list of secrets? Can't we just loop through the secrets
	// below even if it only has a single value?
	// +optional
	SecretRef v1.LocalObjectReference `json:"secretRef,omitempty"`
	// TODO 1: The config can also contain creds, do we just apply it as is?
	// TODO 2: Do we parse a ConfigFile and figure out the sets ourselves or is there something existing already?
	// TODO 3: What's the data key for a ConfigFile? Propose: .ocmconfig
	// TODO 4: ConfigFiles should live in their own SecretRefs type like ConfigFileRefs instead of munging it together
	// with other secrets. Having it separate makes it more user and code friendly, since for specific config files
	// we also need to consider any ConfigSet values. And it makes it easier for the user to keep them apart and for the
	// code to handle them separately.
	// _NOTE_: internal/pkg/ocm/credentials.go already fetches configurations from Secrets by using the key .ocmconfig.
	// Then proceeds with using ApplyConfig for whatever implements this method in the OCM library.
	// Therefore some of this functionality is already implemented in the credentials section.
	// TODO 6: This about separating that out? It makes no different in the light of the context. The only thing missing
	// is looping through all of the secrets.
	// +optional
	SecretRefs []v1.LocalObjectReference `json:"secretRefs,omitempty"`
	// The secrets referred to by SecretRef (or SecretRefs) may contain ocm config data. The ocm config allows to
	// specify sets of configuration data (s. https://ocm.software/docs/cli-reference/help/configfile/). If the
	// SecretRef (or SecretRefs) contain ocm config sets, the user may specify which config set he wants to be
	// effective.
	// TODO 5: There can be several secrets with ocm configuration in it, which one would this refer to?
	// +optional
	ConfigSet string `json:"configSet,omitempty"`
	// +required
	Interval metav1.Duration `json:"interval"`
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// ComponentStatus defines the observed state of Component.
type ComponentStatus struct {
	// +optional
	State string `json:"state,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// The component controller generates an artifact which is a list of component descriptors. If the components were
	// verified, other controllers (e.g. Resource controller) can use this without having to verify the signature again
	// +optional
	ArtifactRef v1.LocalObjectReference `json:"artifactRef,omitempty"`
	// +optional
	Artifact artifactv1.ArtifactSpec `json:"artifact,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	Component ComponentInfo `json:"component,omitempty"`
	// TODO 6: What's the purpose of replicating secrets that are already defined in the spec in the Status?
	// Propagate its effective secrets. Other controllers (e.g. Resource controller) may use this as default
	// if they do not explicitly refer a secret.
	// +optional
	SecretRefs []v1.LocalObjectReference `json:"secretRefs,omitempty"`
	// TODO 7: Same here.
	// The secrets referred to by SecretRef (or SecretRefs) may contain ocm config data. The ocm config allows to
	// specify sets of configuration data (s. https://ocm.software/docs/cli-reference/help/configfile/). If the
	// SecretRef (or SecretRefs) contain ocm config sets, the user may specify which config set he wants to be
	// effective.
	// +optional
	ConfigSet string `json:"configSet,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Component is the Schema for the components API.
type Component struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ComponentSpec   `json:"spec,omitempty"`
	Status ComponentStatus `json:"status,omitempty"`
}

// GetConditions returns the conditions of the Component.
func (in *Component) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the conditions of the Component.
func (in *Component) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

// GetVID unique identifier of the object.
func (in *Component) GetVID() map[string]string {
	vid := fmt.Sprintf("%s:%s", in.Status.Component.Component, in.Status.Component.Version)
	metadata := make(map[string]string)
	metadata[GroupVersion.Group+"/component_version"] = vid

	return metadata
}

func (in *Component) SetObservedGeneration(v int64) {
	in.Status.ObservedGeneration = v
}

func (in *Component) GetObjectMeta() *metav1.ObjectMeta {
	return &in.ObjectMeta
}

func (in *Component) GetKind() string {
	return "Component"
}

// GetRequeueAfter returns the duration after which the ComponentVersion must be
// reconciled again.
func (in Component) GetRequeueAfter() time.Duration {
	return in.Spec.Interval.Duration
}

// +kubebuilder:object:root=true

// ComponentList contains a list of Component.
type ComponentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Component `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Component{}, &ComponentList{})
}

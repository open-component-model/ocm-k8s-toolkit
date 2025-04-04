package v1alpha1

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const KindOCMDeployer = "OCMDeployer"

// OCMDeployerSpec defines the desired state of OCMDeployer.
type OCMDeployerSpec struct {
	// ResourceRef is the k8s resource name of an OCM resource containing the ResourceGroupDefinition.
	// +required
	ResourceRef ObjectKey `json:"resourceRef"`

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

// OCMDeployerStatus defines the observed state of OCMDeployer.
type OCMDeployerStatus struct {
	// ObservedGeneration is the last observed generation of the OCMDeployer
	// object.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions holds the conditions for the OCMDeployer.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// EffectiveOCMConfig specifies the entirety of config maps and secrets
	// whose configuration data was applied to the Resource reconciliation,
	// in the order the configuration data was applied.
	// +optional
	EffectiveOCMConfig []OCMConfiguration `json:"effectiveOCMConfig,omitempty"`
}

func (in *OCMDeployer) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

func (in *OCMDeployer) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

func (in *OCMDeployer) GetVID() map[string]string {
	vid := fmt.Sprintf("%s:%s", in.GetNamespace(), in.GetName())
	metadata := make(map[string]string)
	metadata[GroupVersion.Group+"/resource_version"] = vid

	return metadata
}

func (in *OCMDeployer) SetObservedGeneration(v int64) {
	in.Status.ObservedGeneration = v
}

func (in *OCMDeployer) GetObjectMeta() *metav1.ObjectMeta {
	return &in.ObjectMeta
}

func (in *OCMDeployer) GetKind() string {
	return KindOCMDeployer
}

// GetRequeueAfter returns the duration after which the OCMDeployer must be
// reconciled again.
func (in OCMDeployer) GetRequeueAfter() time.Duration {
	return in.Spec.Interval.Duration
}

func (in *OCMDeployer) GetSpecifiedOCMConfig() []OCMConfiguration {
	return in.Spec.OCMConfig
}

func (in *OCMDeployer) GetEffectiveOCMConfig() []OCMConfiguration {
	return in.Status.EffectiveOCMConfig
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// OCMDeployer is the Schema for the ocmdeployers API.
type OCMDeployer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OCMDeployerSpec   `json:"spec,omitempty"`
	Status OCMDeployerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OCMDeployerList contains a list of OCMDeployer.
type OCMDeployerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OCMDeployer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OCMDeployer{}, &OCMDeployerList{})
}

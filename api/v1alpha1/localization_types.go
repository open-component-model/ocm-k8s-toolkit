package v1alpha1

import (
	"fmt"

	"github.com/fluxcd/pkg/apis/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// LocalizedResource is the Schema for the localizations API.
type LocalizedResource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LocalizedResourceSpec   `json:"spec,omitempty"`
	Status LocalizedResourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// LocalizedResourceList contains a list of LocalizedResource.
type LocalizedResourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LocalizedResource `json:"items"`
}

func (in *LocalizedResource) GetObjectMeta() *metav1.ObjectMeta {
	return &in.ObjectMeta
}

func (in *LocalizedResource) GetKind() string {
	return "LocalizedResource"
}

func (in *LocalizedResource) SetObservedGeneration(v int64) {
	in.Status.ObservedGeneration = v
}

func (in *LocalizedResource) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

func (in *LocalizedResource) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

func (in *LocalizedResource) GetVID() map[string]string {
	vid := fmt.Sprintf("%s:%s", in.Namespace, in.Name)
	metadata := make(map[string]string)
	metadata[GroupVersion.Group+"/localization"] = vid

	return metadata
}

type LocalizedResourceSpec struct {
	// Target that is to be localized
	// +required
	Target ConfigurationReference `json:"target,omitempty"`

	// Config of the localization data to be applied to Target (applied in order of appearance)
	// +required
	Config ConfigurationReference `json:"config,omitempty"`

	// Interval at which to refresh the localization in case the Component Version is not yet ready
	// +required
	Interval metav1.Duration `json:"interval"`

	// Suspend all localization behaviors, but keep existing localizations in place
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

type LocalizationStrategyMapped struct{}

type LocalizationSelector struct {
	// ResId refers to a GVKN/Ns of a resource.
	LocalizationSelectorReference `json:",inline,omitempty" yaml:",inline,omitempty"`

	// AnnotationSelector is a string that follows the label selection expression
	// https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#api
	// It matches with the resource annotations.
	AnnotationSelector string `json:"annotationSelector,omitempty" yaml:"annotationSelector,omitempty"`

	// LabelSelector is a string that follows the label selection expression
	// https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#api
	// It matches with the resource labels.
	LabelSelector string `json:"labelSelector,omitempty" yaml:"labelSelector,omitempty"`
}

// LocalizationSelectorReference is an identifier of a k8s resource object.
type LocalizationSelectorReference struct {
	// Gvk of the resource.
	metav1.GroupVersionKind `json:",inline,omitempty" yaml:",inline,omitempty"`

	// Name of the resource.
	Name string `json:"name,omitempty" yaml:"name,omitempty"`

	// Namespace the resource belongs to, if it can belong to a namespace.
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

// ConfigurationReference defines a configuration which may be accessed through an object in the cluster
// +kubebuilder:validation:MinProperties=1
type ConfigurationReference struct {
	meta.NamespacedObjectKindReference `json:",inline"`
}

type LocalizedResourceStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// The component controller generates an artifact which is a list of component descriptors. If the components were
	// verified, other controllers (e.g. Resource controller) can use this without having to verify the signature again
	// +optional
	ArtifactRef *ObjectKey `json:"artifactRef,omitempty"`

	// A unique digest of the combination of the config and target resources applied through a LocalizationStrategy
	// +optional
	Digest string `json:"digest,omitempty"`
}

func init() {
	SchemeBuilder.Register(&LocalizedResource{}, &LocalizedResourceList{})
}

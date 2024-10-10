package v1alpha1

import (
	"fmt"

	"github.com/fluxcd/pkg/apis/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Localization is the Schema for the localizations API.
type Localization struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LocalizationSpec   `json:"spec,omitempty"`
	Status LocalizationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// LocalizationList contains a list of Localization.
type LocalizationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Localization `json:"items"`
}

func (in *Localization) GetObjectMeta() *metav1.ObjectMeta {
	return &in.ObjectMeta
}

func (in *Localization) GetKind() string {
	return "Localization"
}

func (in *Localization) SetObservedGeneration(v int64) {
	in.Status.ObservedGeneration = v
}

func (in *Localization) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

func (in *Localization) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

func (in *Localization) GetVID() map[string]string {
	vid := fmt.Sprintf("%s:%s", in.Namespace, in.Name)
	metadata := make(map[string]string)
	metadata[GroupVersion.Group+"/localization"] = vid
	return metadata
}

type LocalizationSpec struct {

	// Target that is to be localized
	// +required
	Target LocalizationReference `json:"target,omitempty"`

	// Source of the localization data to be applied to Target (applied in order of appearance)
	// +required
	Source LocalizationSource `json:"source,omitempty"`

	// Interval at which to refresh the localization in case the Component Version is not yet ready
	// +required
	Interval metav1.Duration `json:"interval"`

	// Suspend all localization behaviors, but keep existing localizations in place
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

type LocalizationStrategyType string

const (
	LocalizationStrategyTypeKustomizePatch LocalizationStrategyType = "KustomizePatch"
)

type LocalizationStrategy struct {
	// +required
	Type LocalizationStrategyType `json:"type"`
	// +optional
	*LocalizationStrategyKustomizePatch `json:",inline"`
}

type LocalizationStrategyKustomizePatch struct {
	Patches []LocalizationKustomizePatch `json:"patches"`
}

type LocalizationKustomizePatch struct {
	// Path is a relative file path to the patch_test file.
	Path string `json:"path,omitempty" yaml:"path,omitempty"`

	// Patch is the content of a patch_test.
	Patch string `json:"patch_test,omitempty" yaml:"patch_test,omitempty"`

	// Target points to the resources that the patch_test is applied to
	Target *LocalizationSelector `json:"target,omitempty" yaml:"target,omitempty"`

	// Options is a list of options for the patch_test
	Options map[string]bool `json:"options,omitempty" yaml:"options,omitempty"`
}

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

type LocalizationSource struct {
	LocalizationReference `json:",inline"`
	Strategy              LocalizationStrategy `json:"strategy,omitempty"`
}

// LocalizationReference defines a resource which may be accessed via a snapshot or component version
// +kubebuilder:validation:MinProperties=1
type LocalizationReference struct {
	meta.NamespacedObjectKindReference `json:",inline"`
}

type LocalizationStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// The component controller generates an artifact which is a list of component descriptors. If the components were
	// verified, other controllers (e.g. Resource controller) can use this without having to verify the signature again
	// +optional
	ArtifactRef *ObjectKey `json:"artifactRef,omitempty"`

	// A unique digest of the combination of the source and target resources applied through a LocalizationStrategy
	// +optional
	LocalizationDigest string `json:"localizationDigest,omitempty"`
}

func init() {
	SchemeBuilder.Register(&Localization{}, &LocalizationList{})
}

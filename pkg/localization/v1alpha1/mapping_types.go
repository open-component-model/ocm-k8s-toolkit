package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LocalizationConfig defines a description of a localization.
// It contains the necessary localization rules that can be used in conjunction with a data source to localize resources.
type LocalizationConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              LocalizationSpec `json:"spec"`
}

type LocalizationSpec struct {
	Rules []Rule `json:"rules,omitempty"`
}

type Rule struct {
	Source         Source         `json:"source"`
	Target         Target         `json:"target"`
	Transformation Transformation `json:"transformation"`
}

type Source struct {
	Resource Resource `json:"resource"`
}

type Target struct {
	FileTarget FileTarget `json:"file"`
}

type FileTarget struct {
	Path  string `json:"path"`
	Value string `json:"value"`
}

type Transformation struct {
	Type TransformationType `json:"type"`
}

type TransformationType string

const (
	Registry   TransformationType = "Registry"
	Repository TransformationType = "Repository"
	Image      TransformationType = "Image"
	Tag        TransformationType = "Tag"
)

type Resource struct {
	Name          string            `json:"name"`
	ExtraIdentity map[string]string `json:"extraIdentity,omitempty"`
}

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LocalizationConfig defines a description of a localization.
// It contains the necessary localization rules that can be used in conjunction with a data source to localize resources.
type LocalizationConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              LocalizationConfigSpec `json:"spec"`
}

type LocalizationConfigSpec struct {
	Rules []LocalizationRule `json:"rules,omitempty"`
}

type LocalizationRule struct {
	Source         RuleSource     `json:"source,omitempty"`
	Target         RuleTarget     `json:"target,omitempty"`
	Transformation Transformation `json:"transformation,omitempty"`
}

type RuleSource struct {
	Resource LocalizationResourceReference `json:"resource"`
}

type RuleTarget struct {
	FileTarget FileTarget `json:"file"`
}

type FileTarget struct {
	Path  string `json:"path"`
	Value string `json:"value,omitempty"`
}

type Transformation struct {
	Type       TransformationType        `json:"type"`
	GoTemplate *GoTemplateTransformation `json:"goTemplate,omitempty"`
}

type TransformationType string

const (
	TransformationTypeRegistry   TransformationType = "Registry"
	TransformationTypeRepository TransformationType = "Repository"
	TransformationTypeImage      TransformationType = "Image"
	TransformationTypeTag        TransformationType = "Tag"
	TransformationTypeGoTemplate TransformationType = "GoTemplate"
)

type GoTemplateTransformation struct {
	Data *apiextensionsv1.JSON `json:"data"`
}

type LocalizationResourceReference struct {
	Name          string            `json:"name"`
	ExtraIdentity map[string]string `json:"extraIdentity,omitempty"`
}

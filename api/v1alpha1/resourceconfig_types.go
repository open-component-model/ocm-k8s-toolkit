package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const KindResourceConfig = "ResourceConfig"

// +kubebuilder:object:root=true

// ResourceConfig defines a set of rules that instruct on how to configure a Resource.
// It is usd within the ConfiguredResource to structure where values should be inserted.
// It contains the necessary localization rules that can be used in conjunction with a data source to localize resources.
type ResourceConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ResourceConfigSpec `json:"spec"`
}

func (in *ResourceConfig) GetRules() []ConfigurationRule {
	return in.Spec.Rules
}

// +kubebuilder:object:root=true

// ResourceConfigList contains a list of ResourceConfig.
type ResourceConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ResourceConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ResourceConfig{}, &ResourceConfigList{})
}

type ResourceConfigSpec struct {
	Rules []ConfigurationRule `json:"rules"`
}

// ConfigurationRule defines a rule that can be used to configure resources.
// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:MaxProperties=1
type ConfigurationRule struct {
	YAMLSubstitution *ConfigurationRuleYAMLSubstitution `json:"yamlsubst,omitempty"`
	GoTemplate       *ConfigurationRuleGoTemplate       `json:"goTemplate,omitempty"`
}

type ConfigurationRuleYAMLSubstitution struct {
	Source ConfigurationRuleYAMLSubsitutionSource `json:"source"`
	Target ConfigurationRuleYAMLSubsitutionTarget `json:"target"`
}

type ConfigurationRuleYAMLSubsitutionSource struct {
	// Value is the value that will be used to replace the target in the file.
	Value string `json:"value"`
}

type ConfigurationRuleYAMLSubsitutionTarget struct {
	// File is used to identify the file where the rule will apply its data to
	File FileTargetWithValue `json:"file"`
}

type ConfigurationRuleGoTemplate struct {
	// FileTarget is used to identify the file where the rule will apply its data to (parse the GoTemplate)
	FileTarget FileTarget `json:"file"`
	// GoTemplateData is an arbitrary object that is forwarded to the GoTemplate for use as a struct.
	//
	// Example:
	//
	//	goTemplate:
	//	  data:
	//	    key: value
	//
	// This would then lead to a struct that can be used in the GoTemplate (assuming standard Delimiters):
	//
	//	{{ .key }}
	Data *apiextensionsv1.JSON `json:"data,omitempty"`

	Delimiters *GoTemplateDelimiters `json:"delimiters,omitempty"`
}

// ConfigurationRuleSource describes a source of information where the rule will get its data from.
// Currently only ValueSource is supported.
type ConfigurationRuleSource struct {
	ValueSource ValueSource `json:"value"`
}

type ValueSource string

// ConfigurationRuleTarget describes a target where the rule will store its data.
// Currently only FileTargetWithValue is supported.
type ConfigurationRuleTarget struct {
	// The File target is used to identify the file where the rule will apply its sources to after considering
	// the transformation.
	FileTarget FileTargetWithValue `json:"file"`
}

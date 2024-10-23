package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	Rules []ConfigurationRule `json:"rules,omitempty"`
}

type ConfigurationRule struct {
	Source ConfigurationRuleSource `json:"source"`
	Target ConfigurationRuleTarget `json:"target"`
}

// ConfigurationRuleSource describes a source of information where the rule will get its data from.
// Currently only ValueSource is supported.
type ConfigurationRuleSource struct {
	ValueSource ValueSource `json:"value,omitempty"`
}

type ValueSource string

// ConfigurationRuleTarget describes a target where the rule will store its data.
// Currently only FileTarget is supported.
type ConfigurationRuleTarget struct {
	// The File target is used to identify the file where the rule will apply its sources to after considering
	// the transformation.
	FileTarget FileTarget `json:"file"`
}

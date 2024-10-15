package v1alpha1

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/opencontainers/go-digest"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	yaml "sigs.k8s.io/yaml/goyaml.v3"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// LocalizationConfig defines a description of a localization.
// It contains the necessary localization rules that can be used in conjunction with a data source to localize resources.
type LocalizationConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              LocalizationConfigSpec `json:"spec"`
}

func (in *LocalizationConfig) Open() (io.ReadCloser, error) {
	buf, err := in.asBuf()
	if err != nil {
		return nil, err
	}

	return io.NopCloser(buf), nil
}

func (in *LocalizationConfig) asBuf() (*bytes.Buffer, error) {
	var buf bytes.Buffer
	if err := yaml.NewDecoder(&buf).Decode(in); err != nil {
		return nil, err
	}

	return &buf, nil
}

func (in *LocalizationConfig) UnpackIntoDirectory(path string) error {
	buf, err := in.asBuf()
	if err != nil {
		return err
	}

	return os.WriteFile(fmt.Sprintf("%s-%s.yaml", path, in.Name), buf.Bytes(), os.ModePerm)
}

func (in *LocalizationConfig) GetDigest() string {
	buf, err := in.asBuf()
	if err != nil {
		panic(err)
	}

	return digest.NewDigestFromBytes(digest.SHA256, buf.Bytes()).String()
}

func (in *LocalizationConfig) GetRevision() string {
	return fmt.Sprintf("ResourceVersion: %s", in.GetResourceVersion())
}

type LocalizationConfigSpec struct {
	Rules []LocalizationRule `json:"rules,omitempty"`
}

type LocalizationRule struct {
	Source         RuleSource     `json:"source,omitempty"`
	Target         RuleTarget     `json:"target"`
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
	Data       *apiextensionsv1.JSON `json:"data,omitempty"`
	Delimiters *GoTemplateDelimiters `json:"delimiters,omitempty"`
}

type GoTemplateDelimiters struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}

type LocalizationResourceReference struct {
	Name          string            `json:"name"`
	ExtraIdentity map[string]string `json:"extraIdentity,omitempty"`
}

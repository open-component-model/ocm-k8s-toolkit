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

	return os.WriteFile(fmt.Sprintf("%s-%s.yaml", path, in.Name), buf.Bytes(), 0o600)
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

// LocalizationConfigSpec defines the desired state of LocalizationConfig.
// It contains the necessary localization rules that can be used in conjunction with a data source to localize resources.
// For more information, see the LocalizationRule type.
type LocalizationConfigSpec struct {
	Rules []LocalizationRule `json:"rules,omitempty"`
}

// LocalizationRule defines a rule that can be used to localize resources.
// Each rule contains a source reference, a target reference, and a transformation, with each of these fields
// being omittable except for target.
type LocalizationRule struct {
	// The Source reference is used to identify the resource that should be localized.
	// It will be used to extract the necessary information to localize the target reference.
	// A resource reference will always look inside the same component as the component that is being localized.
	//
	// Example:
	//	- source:
	//		resource:
	//			name: image
	Source RuleSource `json:"source,omitempty"`
	// The Target reference is used to identify the content inside the resource that should be localized.
	// It will be used to determine where the localized content should be stored.
	// To store the image fetched from source into a file called values.yaml inside deploy.image, one can use the following target:
	//
	//   - target:
	//     file:
	//       path: values.yaml
	//       value: deploy.image
	Target RuleTarget `json:"target"`
	// The Transformation is used to tell the Localization additional information about how to localize the content.
	// The transformation can be used to digest the source in a different way or interpret the rule differently.
	// A simple example of this is the TransformationTypeImage (also the default if no Transformation has been specified),
	// which extracts the full image reference:
	//  - transformation:
	//      type: Image
	//
	// An example of this is the GoTemplate transformation, which allows the user to use Go templates to transform the source.
	// With this transformation, one can switch from using the Target and fully customize their localization:
	//  - transformation:
	//      type: GoTemplate
	//
	// For more information on individual TransformationType's, see their respective documentation.
	Transformation Transformation `json:"transformation,omitempty"`
}

// RuleSource describes a source of information where the rule will get its data from.
// Currently only LocalizationResourceReference is supported.
type RuleSource struct {
	// The Resource reference is used to identify the resource will be used to fill in the target reference.
	// If one has a ComponentDescriptor with 2 resources, one can use this to reference between them.
	// For a Component Descriptor with two resources, a "deployment-instruction" (to be localized)
	// and an "image" (to be localized from), one can use the following source:
	// - source:
	//     resource:
	//       name: image
	//
	// The localization will then look into the corresponding descriptor and resolve its AccessType:
	// components:
	//  - component:
	//      # ...
	//      resources:
	//        - access:
	//            imageReference: ghcr.io/stefanprodan/podinfo:6.2.0
	//            type: ociArtifact
	//          name: image
	//          relation: external
	//          type: ociImage
	//          version: 6.2.0
	//      sources: []
	//      version: 1.0.0
	//    meta:
	//      schemaVersion: v2
	//
	// This would then lead to a value of "ghcr.io/stefanprodan/podinfo:6.2.0".
	Resource LocalizationResourceReference `json:"resource"`
}

// RuleTarget describes a target where the rule will store its data.
// Currently only FileTarget is supported.
type RuleTarget struct {
	// The File target is used to identify the file where the rule will apply its sources to after considering
	// the transformation.
	FileTarget FileTarget `json:"file"`
}

// FileTarget describes a file inside the Resource that is currently being localized.
// It can contain a path and a value, where Path is the filepath (relative to the Resource) to the file inside the resource
// and the value is a reference to the content that should be localized.
// If one wants to store the image fetched from source into a file called values.yaml inside deploy.image,
// one can use the following value:
//   - target:
//     file:
//     value: deploy.image
//     path: values.yaml
type FileTarget struct {
	// The Path is the filepath (relative to the Resource) to the file inside the resource.
	Path string `json:"path"`
	// The Value is a reference to the content that should be localized.
	Value string `json:"value,omitempty"`
}

type Transformation struct {
	Type       TransformationType        `json:"type"`
	GoTemplate *GoTemplateTransformation `json:"goTemplate,omitempty"`
}

type TransformationType string

const (
	// TransformationTypeImage is a transformation that extracts the full image reference.
	// This is the default transformation type.
	// Example: "docker.io/library/nginx:latest" -> "docker.io/library/nginx:latest".
	TransformationTypeImage TransformationType = "Image"
	// TransformationTypeImageNoTag is a transformation that extracts the image reference without the tag.
	// Example: "docker.io/library/nginx:latest" -> "docker.io/library/nginx".
	TransformationTypeImageNoTag TransformationType = "ImageNoTag"
	// TransformationTypeRegistry is a transformation that extracts the registry part of an image reference.
	// Example: "docker.io/library/nginx:latest" -> "docker.io".
	TransformationTypeRegistry TransformationType = "Registry"
	// TransformationTypeRepository is a transformation that extracts the repository part of an image reference.
	// Example: "docker.io/library/nginx:latest" -> "library/nginx".
	TransformationTypeRepository TransformationType = "Repository"
	// TransformationTypeTag is a transformation that extracts the tag part of an image reference.
	// Example: "docker.io/library/nginx:latest" -> "latest".
	TransformationTypeTag TransformationType = "Tag"
	// TransformationTypeGoTemplate is a transformation that uses Go templates.
	// This transformation type does not support source references or target path references.
	// TODO Add support for this case so someone could write something like "{{ .Repository }}{{ .Tag }}".
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

package v1alpha1

import (
	"github.com/fluxcd/pkg/apis/meta"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	ocmv1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
)

type ConfigurationPolicy string

const (
	ConfigurationPolicyPropagate      ConfigurationPolicy = "Propagate"
	ConfigurationPolicyDoNotPropagate ConfigurationPolicy = "DoNotPropagate"
)

// OCMConfiguration defines a configuration applied to the reconciliation of an
// ocm k8s object as well as the policy for its propagation of this
// configuration.
// +kubebuilder:validation:XValidation:rule="((!has(self.apiVersion) || self.apiVersion == \"\" || self.apiVersion == \"v1\") && (self.kind == \"Secret\" || self.kind == \"ConfigMap\")) || (self.apiVersion == \"delivery.ocm.software/v1alpha1\" && (self.kind == \"Repository\" || self.kind == \"Component\" || self.kind == \"Resource\" || self.kind == \"Replication\"))",message="apiVersion must be one of \"v1\" with kind \"Secret\" or \"ConfigMap\" or \"delivery.ocm.software/v1alpha1\" with the kind of an OCM kubernetes object"
type OCMConfiguration struct {
	// Ref reference config maps or secrets containing arbitrary
	// ocm config data (in the ocm config file format), or other configurable
	// ocm api objects (Repository, Component, Resource) to
	// reuse their propagated configuration.
	meta.NamespacedObjectKindReference `json:",inline"`
	// Policy affects the propagation behavior of the configuration. If set to
	// ConfigurationPolicyPropagate other ocm api objects can reference this
	// object to reuse this configuration.
	// +kubebuilder:validation:Enum:="Propagate";"DoNotPropagate"
	// +kubebuilder:default:="Propagate"
	// +required
	Policy ConfigurationPolicy `json:"policy,omitempty"`
}

type ObjectKey struct {
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// +required
	Name string `json:"name,omitempty"`
}

type Verification struct {
	// +required
	Signature string `json:"signature,omitempty"`
	// Public Key Secret Format
	// A secret containing public keys for signature verification is expected to be of the structure:
	//
	//  Data:
	//	  <Signature-Name>: <PublicKey/Certificate>
	//
	// Additionally, to prepare for a common ocm secret management, it might make sense to introduce a specific secret type
	// for these secrets.
	// +optional
	SecretRef corev1.LocalObjectReference `json:"secretRef,omitempty"`
	// Value defines a PEM/base64 encoded public key value.
	// +optional
	Value string `json:"value,omitempty"`
}

// ResourceID defines the configuration of the repository.
type ResourceID struct {
	// +required
	ByReference ResourceReference `json:"byReference,omitempty"`
	// TODO: Implement BySelector (see https://github.com/open-component-model/ocm-project/issues/296)
}

// ResourceReference defines a reference to a resource akin to the OCM Specification.
// For more details see dedicated guide in the Specification:
// https://github.com/open-component-model/ocm-spec/blob/main/doc/05-guidelines/03-references.md#references
type ResourceReference struct {
	Resource      ocmv1.Identity   `json:"resource"`
	ReferencePath []ocmv1.Identity `json:"referencePath,omitempty"`
}

type ComponentInfo struct {
	// +required
	RepositorySpec *apiextensionsv1.JSON `json:"repositorySpec,omitempty"`
	// +required
	Component string `json:"component,omitempty"`
	// +required
	Version string `json:"version,omitempty"`
}

type ResourceInfo struct {
	// +required
	Name string `json:"name,omitempty"`
	// +required
	Type string `json:"type,omitempty"`
	// +optional
	Version string `json:"version,omitempty"`
	// +optional
	ExtraIdentity map[string]string `json:"extraIdentity,omitempty"`
	// +required
	Access apiextensionsv1.JSON `json:"access,omitempty"`
	// +required
	Digest string `json:"digest,omitempty"`
}

type SourceReference struct {
	// +required
	Registry string `json:"registry"`
	// +required
	Repository string `json:"repository"`
	// +optional
	Reference string `json:"reference"`
	// +optional
	Digest string `json:"digest"`
	// +optional
	Tag string `json:"tag"`
}

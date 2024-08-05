package v1alpha1

import (
	"encoding/json"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

type ObjectKey struct {
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// +required
	Name string `json:"name,omitempty"`
}

type Verification struct {
	// +required
	Signature string `json:"signature,omitempty"`
	// +optional
	SecretRef string `json:"secretRef,omitempty"`
	// +optional
	Value string `json:"value,omitempty"`
}

// RepositorySpec defines the configuration of the repository.
type ResourceId struct {
	// +required
	Name string `json:"name,omitempty"`
	// +optional
	ExtraIdentity map[string]string `json:"extraIdentity,omitempty"`
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
	Access Access `json:"access,omitempty"`
}

type Access json.RawMessage

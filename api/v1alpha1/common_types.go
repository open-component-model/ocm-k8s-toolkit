/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	ocmv1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
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

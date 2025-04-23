package ociartifact

import "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"

// OCIArtifactCreator is an interface for objects that can create OCI artifacts.
type Provider interface {
	GetOCIArtifact() *v1alpha1.OCIArtifactInfo
	GetOCIRepository() string
	GetManifestDigest() string
}

package types

import (
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SecretRefProvider are objects that provide secret refs. The interface allows all implementers to use the same
// function to retrieve its secrets.
//
// GetEffectiveSecretRefs returns references to the secrets that were effectively available for that object.
// For example, the ComponentSpec's secret ref and secret refs might be empty, but the OCMRepositorySpec of the
// OCMRepository the Component references, might have a secret ref or secret refs specified. If this is the case
// (so the ComponentSpec does not specify any secret refs but the OCMRepositorySpec does), the Component inherits
// (or defaults to) these secrets.
// For OCMRepository, GetSecretRefs() and GetEffectiveSecretRefs() would then return the same thing. For Component,
// GetSecretRefs() would return an empty list while GetEffectiveSecretRefs() would return the same thing as the
// OCMRepository's GetSecretRefs() and GetEffectiveSecretRefs().
// Each SecretRefProvider exposes its effective secrets in its status (see e.g. OCMRepository.Status). This way,
// controllers such as the Resource controller does not have to backtrack the entire kubernetes object chain to
// OCMRepository to read its defaults.
// +kubebuilder:object:generate=false
type SecretRefProvider interface {
	client.Object

	// GetSecretRefs return the list of all secret references specified in the spec of the implementing object.
	GetSecretRefs() []corev1.LocalObjectReference

	// GetEffectiveSecretRefs returns the list of all secret references specified in the spec of the implementing object.
	GetEffectiveSecretRefs() []corev1.LocalObjectReference
}

// ConfigRefProvider are objects that provide secret refs. The interface allows all implementers to use the same
// function to retrieve its secrets.
//
// For a detailed explanation, see SecretRefProvider.
// +kubebuilder:object:generate=false
type ConfigRefProvider interface {
	client.Object
	GetConfigRefs() []corev1.LocalObjectReference
	GetEffectiveConfigRefs() []corev1.LocalObjectReference
}

// ConfigSetProvider are objects that may contain config sets. The interface allows all implementers to use the same
// function to retrieve its config set.
//
// GetConfigSet() returns a string pointer because we have to distinguish between a purposefully set empty value and a
// unset value to determine whether to use the default.
//
// For a detailed explanation, see SecretRefProvider.
// +kubebuilder:object:generate=false
type ConfigSetProvider interface {
	client.Object
	GetConfigSet() *string
	GetEffectiveConfigSet() string
}

// OCMK8SObject is a composite interface that the ocm-k8s-toolkit resources implement which allows them to use
// the same ocm context configuration function.
// +kubebuilder:object:generate=false
type OCMK8SObject interface {
	conditions.Setter
	SecretRefProvider
	ConfigRefProvider
	ConfigSetProvider
}

// VerificationProvider are objects that may provide verification information. The interface allows all implementers to
// use the same function to retrieve and parse the contained or referenced public keys.
// +kubebuilder:object:generate=false
type VerificationProvider interface {
	client.Object
	GetVerifications() []v1alpha1.Verification
}

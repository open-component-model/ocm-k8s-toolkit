package ocm

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/resolvers"
	"ocm.software/ocm/api/ocm/tools/signing"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// Verification is an internal representation of v1alpha1.Verification where the public key is already extracted from
// the value or secret.
type Verification struct {
	Signature string
	PublicKey []byte
}

func GetVerifications(ctx context.Context, client ctrl.Client,
	obj v1alpha1.VerificationProvider,
) ([]Verification, error) {
	verifications := obj.GetVerifications()

	var err error
	var secret corev1.Secret
	v := make([]Verification, 0, len(verifications))
	for _, verification := range verifications {
		internal := Verification{
			Signature: verification.Signature,
		}
		if verification.Value == "" && verification.SecretRef.Name == "" {
			return nil, reconcile.TerminalError(fmt.Errorf("value and secret ref cannot both be empty for signature: %s", verification.Signature))
		}
		if verification.Value != "" && verification.SecretRef.Name != "" {
			return nil, reconcile.TerminalError(fmt.Errorf("value and secret ref cannot both be set for signature: %s", verification.Signature))
		}
		if verification.Value != "" {
			internal.PublicKey, err = base64.StdEncoding.DecodeString(verification.Value)
			if err != nil {
				return nil, err
			}
		}
		if verification.SecretRef.Name != "" {
			err = client.Get(ctx, ctrl.ObjectKey{Namespace: obj.GetNamespace(), Name: verification.SecretRef.Name}, &secret)
			if err != nil {
				return nil, err
			}
			if certBytes, ok := secret.Data[verification.Signature]; ok {
				internal.PublicKey = certBytes
			}
		}

		v = append(v, internal)
	}

	return v, nil
}

func VerifyComponentVersion(ctx context.Context, cv ocm.ComponentVersionAccess, sigs []string) (*Descriptors, error) {
	logger := log.FromContext(ctx).WithName("signature-validation")

	if len(sigs) == 0 || cv == nil {
		return nil, nil
	}
	octx := cv.GetContext()

	resolver := resolvers.NewCompoundResolver(cv.Repository(), octx.GetResolver())
	opts := signing.NewOptions(
		signing.Resolver(resolver),
		// TODO: Consider configurable options for digest verification (@frewilhelm @fabianburth)
		//   https://github.com/open-component-model/ocm-k8s-toolkit/issues/208
		// do we really want to verify the digests here? isn't it sufficient to verify the signatures since
		// the digest verification can and has to be done anyways by the resource controller?
		// signing.VerifyDigests(),
		signing.VerifySignature(sigs...),
		signing.Recursive(),
	)

	ws := signing.DefaultWalkingState(cv.GetContext())
	_, err := signing.Apply(nil, ws, cv, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to verify component signatures %s: %w", strings.Join(sigs, ", "), err)
	}
	logger.Info("successfully verified component signature")

	return &Descriptors{List: signing.ListComponentDescriptors(cv, ws)}, nil
}

func VerifyComponentVersionAndListDescriptors(
	ctx context.Context,
	octx ocm.Context,
	cv ocm.ComponentVersionAccess,
	sigs []string,
) (*Descriptors, error) {
	descriptors, err := VerifyComponentVersion(ctx, cv, sigs)
	if err != nil {
		return nil, fmt.Errorf("failed to verify component: %w", err)
	}

	// if the component descriptors were not collected during signature validation, collect them now
	if descriptors == nil || len(descriptors.List) == 0 {
		descriptors, err = ListComponentDescriptors(ctx, cv, resolvers.NewCompoundResolver(cv.Repository(), octx.GetResolver()))
		if err != nil {
			return nil, fmt.Errorf("failed to list component descriptors: %w", err)
		}
	}

	return descriptors, nil
}

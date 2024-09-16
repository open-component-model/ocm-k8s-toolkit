package ocm

import (
	"context"
	"encoding/base64"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/rerror"
)

func GetVerifications(ctx context.Context, client ctrl.Client,
	obj v1alpha1.VerificationProvider,
) ([]Verification, rerror.ReconcileError) {
	verifications := obj.GetVerifications()

	var err error
	var secret corev1.Secret
	v := make([]Verification, 0, len(verifications))
	for index, verification := range verifications {
		internal := Verification{
			Signature: verification.Signature,
		}
		if verification.Value == "" && verification.SecretRef.Name == "" {
			return nil, rerror.AsNonRetryableError(fmt.Errorf("value and secret ref cannot both be empty for signature: %s", verification.Signature))
		}
		if verification.Value != "" && verification.SecretRef.Name != "" {
			return nil, rerror.AsNonRetryableError(fmt.Errorf("value and secret ref cannot both be set for signature: %s", verification.Signature))
		}
		if verification.Value != "" {
			internal.PublicKey, err = base64.StdEncoding.DecodeString(verification.Value)
			if err != nil {
				return nil, rerror.AsNonRetryableError(err)
			}
		}
		if verification.SecretRef.Name != "" {
			err = client.Get(ctx, ctrl.ObjectKey{Namespace: obj.GetNamespace(), Name: verification.SecretRef.Name}, &secret)
			if err != nil {
				return nil, rerror.AsRetryableError(err)
			}
			if certBytes, ok := secret.Data[verification.Signature]; ok {
				internal.PublicKey = certBytes
			}
		}
		v[index] = internal
	}

	return v, nil
}

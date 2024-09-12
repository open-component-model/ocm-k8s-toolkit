package helpers

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/open-component-model/ocm-k8s-toolkit/utils/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/utils/rerror"
	"github.com/open-component-model/ocm-k8s-toolkit/utils/types"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
)

func GetVerifications(ctx context.Context, client ctrl.Client,
	obj types.VerificationProvider,
) ([]ocm.Verification, rerror.ReconcileError) {
	verifications := obj.GetVerifications()

	var err error
	var secret corev1.Secret
	v := make([]ocm.Verification, 0, len(verifications))
	for index, verification := range verifications {
		internal := ocm.Verification{
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

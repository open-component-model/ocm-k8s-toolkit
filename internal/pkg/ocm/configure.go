package ocm

import (
	"context"
	"fmt"

	ocmctx "ocm.software/ocm/api/ocm"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/rerror"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/status"
)

func ConfigureOCMContext(ctx context.Context, r Reconciler, octx ocmctx.Context,
	obj deliveryv1alpha1.OCMK8SObject, def deliveryv1alpha1.OCMK8SObject,
) rerror.ReconcileError {
	secrets, err := GetSecrets(ctx, r.GetClient(), GetEffectiveSecretRefs(ctx, obj, def))
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), obj, deliveryv1alpha1.SecretFetchFailedReason, err.Error())

		return rerror.AsRetryableError(fmt.Errorf("failed to get secrets: %w", err))
	}

	configs, err := GetConfigMaps(ctx, r.GetClient(), GetEffectiveConfigRefs(ctx, obj, def))
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), obj, deliveryv1alpha1.ConfigFetchFailedReason, err.Error())

		return rerror.AsRetryableError(fmt.Errorf("failed to get configmaps: %w", err))
	}

	set := GetEffectiveConfigSet(ctx, obj, def)

	var rerr rerror.ReconcileError
	var signingkeys []Verification
	if vprov, ok := obj.(deliveryv1alpha1.VerificationProvider); ok {
		signingkeys, rerr = GetVerifications(ctx, r.GetClient(), vprov)
		if rerr != nil {
			status.MarkNotReady(r.GetEventRecorder(), obj, deliveryv1alpha1.VerificationsInvalidReason, rerr.Error())

			return rerr
		}
	}

	err = ConfigureContext(ctx, octx, signingkeys, secrets, configs, set)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), obj, deliveryv1alpha1.ConfigureContextFailedReason, err.Error())

		return rerror.AsNonRetryableError(fmt.Errorf("failed to configure ocm context: %w", err))
	}

	return nil
}

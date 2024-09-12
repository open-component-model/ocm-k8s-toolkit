package helpers

import (
	"context"
	"fmt"

	"github.com/open-component-model/ocm-k8s-toolkit/utils/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/utils/rerror"
	"github.com/open-component-model/ocm-k8s-toolkit/utils/types"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/status"
	ocmctx "ocm.software/ocm/api/ocm"
)

func ConfigureOCMContext(ctx context.Context, r OCMK8SReconciler, octx ocmctx.Context,
	obj types.OCMK8SObject, def types.OCMK8SObject,
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
	var signingkeys []ocm.Verification
	if vprov, ok := obj.(types.VerificationProvider); ok {
		signingkeys, rerr = GetVerifications(ctx, r.GetClient(), vprov)
		if rerr != nil {
			status.MarkNotReady(r.GetEventRecorder(), obj, deliveryv1alpha1.VerificationsInvalidReason, rerr.Error())

			return rerr
		}
	}

	err = ocm.ConfigureContext(ctx, octx, signingkeys, secrets, configs, set)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), obj, deliveryv1alpha1.ConfigureContextFailedReason, err.Error())

		return rerror.AsNonRetryableError(fmt.Errorf("failed to configure ocm context: %w", err))
	}

	return nil
}

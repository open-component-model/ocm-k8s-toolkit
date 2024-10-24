package ocm

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"ocm.software/ocm/api/ocm"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

func ConfigureOCMContext(ctx context.Context, r Reconciler, octx ocm.Context,
	obj v1alpha1.OCMK8SObject, def v1alpha1.OCMK8SObject,
) error {
	secrets, err := GetSecrets(ctx, r.GetClient(), GetEffectiveSecretRefs(ctx, obj, def))
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), obj, v1alpha1.SecretFetchFailedReason, err.Error())

		return fmt.Errorf("failed to get secrets: %w", err)
	}

	configs, err := GetConfigMaps(ctx, r.GetClient(), GetEffectiveConfigRefs(ctx, obj, def))
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), obj, v1alpha1.ConfigFetchFailedReason, err.Error())

		return fmt.Errorf("failed to get configmaps: %w", err)
	}

	set := GetEffectiveConfigSet(ctx, obj, def)

	var signingkeys []Verification
	if vprov, ok := obj.(v1alpha1.VerificationProvider); ok {
		signingkeys, err = GetVerifications(ctx, r.GetClient(), vprov)
		if err != nil {
			status.MarkNotReady(r.GetEventRecorder(), obj, v1alpha1.VerificationsInvalidReason, err.Error())

			return err
		}
	}

	err = ConfigureContext(ctx, octx, signingkeys, secrets, configs, set)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), obj, v1alpha1.ConfigureContextFailedReason, err.Error())

		return reconcile.TerminalError(fmt.Errorf("failed to configure ocm context: %w", err))
	}

	return nil
}

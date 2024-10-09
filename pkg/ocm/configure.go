package ocm

import (
	"context"
	"errors"
	"fmt"
	"github.com/fluxcd/pkg/runtime/conditions"
	"k8s.io/apimachinery/pkg/types"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"ocm.software/ocm/api/ocm"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/rerror"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

func ConfigureOCMContext(ctx context.Context, r Reconciler, octx ocm.Context,
	obj v1alpha1.OCMK8SObject, def v1alpha1.OCMK8SObject,
) rerror.ReconcileError {
	secrets, err := GetSecrets(ctx, r.GetClient(), GetEffectiveSecretRefs(ctx, obj, def))
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), obj, v1alpha1.SecretFetchFailedReason, err.Error())

		return rerror.AsRetryableError(fmt.Errorf("failed to get secrets: %w", err))
	}

	configs, err := GetConfigMaps(ctx, r.GetClient(), GetEffectiveConfigRefs(ctx, obj, def))
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), obj, v1alpha1.ConfigFetchFailedReason, err.Error())

		return rerror.AsRetryableError(fmt.Errorf("failed to get configmaps: %w", err))
	}

	set := GetEffectiveConfigSet(ctx, obj, def)

	// TODO: factor out the signing part - the required type assertion should
	//   have been enough of a hint for that all along. This will allow to get
	//   rid of the VerificationProvider interface.
	var rerr rerror.ReconcileError
	var signingkeys []Verification
	if vprov, ok := obj.(v1alpha1.VerificationProvider); ok {
		signingkeys, rerr = GetVerifications(ctx, r.GetClient(), vprov)
		if rerr != nil {
			status.MarkNotReady(r.GetEventRecorder(), obj, v1alpha1.VerificationsInvalidReason, rerr.Error())

			return rerr
		}
	}

	err = ConfigureContext(ctx, octx, signingkeys, secrets, configs, set)
	if err != nil {
		status.MarkNotReady(r.GetEventRecorder(), obj, v1alpha1.ConfigureContextFailedReason, err.Error())

		return rerror.AsNonRetryableError(fmt.Errorf("failed to configure ocm context: %w", err))
	}

	return nil
}

func ConfigureResolvers(ctx context.Context, client k8sclient.Client,
	octx ocm.Context, resolvers []v1alpha1.Resolver) rerror.ReconcileError {

	logger := log.FromContext(ctx)

	for _, resolver := range resolvers {
		resolverRepo := &v1alpha1.OCMRepository{}
		if err := client.Get(ctx, types.NamespacedName{
			Namespace: resolver.RepositoryRef.Namespace,
			Name:      resolver.RepositoryRef.Name,
		}, resolverRepo); err != nil {
			logger.Info("failed to get resolver repository")

			return rerror.AsRetryableError(fmt.Errorf("failed to get resolver repository: %w", err))
		}

		if resolverRepo.DeletionTimestamp != nil {
			return rerror.AsNonRetryableError(errors.New("repository is being deleted, please do not use it"))
		}

		if !conditions.IsReady(resolverRepo) {
			logger.Info("repository is not ready", "name", resolver.RepositoryRef.Name)

			return rerror.AsRetryableError(errors.New("repository is not ready"))
		}
		resolverRepoSpec, err := octx.RepositorySpecForConfig(resolverRepo.Spec.RepositorySpec.Raw, nil)
		if err != nil {
			return rerror.AsNonRetryableError(fmt.Errorf("failed to unmarshal RepositorySpec: %w", err))
		}

		octx.AddResolverRule(resolver.Prefix, resolverRepoSpec, resolver.Priority)
	}

	return nil
}

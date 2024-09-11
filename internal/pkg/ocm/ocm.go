package ocm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/mandelsoft/goutils/matcher"
	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"ocm.software/ocm/api/credentials/extensions/repositories/dockerconfig"
	"ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/compdesc"
	"ocm.software/ocm/api/ocm/extensions/attrs/signingattr"
	utils "ocm.software/ocm/api/ocm/ocmutils"
	"ocm.software/ocm/api/ocm/resolvers"
	"ocm.software/ocm/api/ocm/tools/signing"
	common "ocm.software/ocm/api/utils/misc"
	"ocm.software/ocm/api/utils/runtime"
	"ocm.software/ocm/api/utils/semverutils"

	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// TODO: This function should be almost entirely replaced by the ocm k8s secret
//  manager once it is ready!

type Verification struct {
	Signature string
	PublicKey []byte
}

// ConfigureContext reads all the secrets and config maps, checks them for
// known configuration types and applies them to the context.
func ConfigureContext(ctx context.Context, octx ocm.Context, verifications []Verification,
	secrets []*corev1.Secret, configmaps []*corev1.ConfigMap, configset ...string,
) error {
	err := ConfigureContextForSecrets(ctx, octx, secrets)
	if err != nil {
		return err
	}
	err = ConfigureContextForConfigMaps(ctx, octx, configmaps)
	if err != nil {
		return err
	}

	var set string
	if len(configset) > 0 {
		set = configset[0]
	}
	if set != "" {
		err := octx.ConfigContext().ApplyConfigSet(set)
		if err != nil {
			return fmt.Errorf("cannot apply ocm config set %s: %w", set, err)
		}
	}

	// If we were to introduce further functionality into the controller that have to use the signing registry we
	// retrieve from the context here (e.g. signing), we would have to change the coding so that the signing operation
	// and the verification operation use dedicated signing stores.
	signinfo := signingattr.Get(octx)

	for _, verification := range verifications {
		signinfo.RegisterPublicKey(verification.Signature, verification.PublicKey)
	}

	return nil
}

func ConfigureContextForSecrets(_ context.Context, octx ocm.Context, secrets []*corev1.Secret) error {
	history := map[ctrl.ObjectKey]struct{}{}
	for _, secret := range secrets {
		// track that the list does not contain the same secret twice as this could lead to unexpected behavior
		key := ctrl.ObjectKeyFromObject(secret)
		if _, ok := history[key]; ok {
			return fmt.Errorf("the same secret cannot be referenced twice")
		}
		history[key] = struct{}{}

		if dockerConfigBytes, ok := secret.Data[corev1.DockerConfigJsonKey]; ok {
			if len(dockerConfigBytes) > 0 {
				spec := dockerconfig.NewRepositorySpecForConfig(dockerConfigBytes, true)

				if _, err := octx.CredentialsContext().RepositoryForSpec(spec); err != nil {
					return fmt.Errorf("cannot create credentials from secret: %w", err)
				}
			}
		}

		if ocmConfigBytes, ok := secret.Data[deliveryv1alpha1.OCMConfigKey]; ok {
			if len(ocmConfigBytes) > 0 {
				cfg, err := octx.ConfigContext().GetConfigForData(ocmConfigBytes, runtime.DefaultYAMLEncoding)
				if err != nil {
					return err
				}

				err = octx.ConfigContext().ApplyConfig(cfg, fmt.Sprintf("ocm config secret: %s/%s",
					secret.Namespace, secret.Name))
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func ConfigureContextForConfigMaps(_ context.Context, octx ocm.Context, configmaps []*corev1.ConfigMap) error {
	history := map[ctrl.ObjectKey]struct{}{}
	for _, configmap := range configmaps {
		// track that the list does not contain the same configmap twice as this could lead to unexpected behavior
		key := ctrl.ObjectKeyFromObject(configmap)
		if _, ok := history[key]; ok {
			return fmt.Errorf("the same secret cannot be referenced twice")
		}
		history[key] = struct{}{}

		ocmConfigData, ok := configmap.Data[deliveryv1alpha1.OCMConfigKey]
		if !ok {
			return fmt.Errorf("ocm configuration config map does not contain key \"%s\"",
				deliveryv1alpha1.OCMConfigKey)
		}
		if len(ocmConfigData) > 0 {
			cfg, err := octx.ConfigContext().GetConfigForData([]byte(ocmConfigData), nil)
			if err != nil {
				return fmt.Errorf("invalid ocm config in \"%s\" in namespace \"%s\": %w",
					configmap.Name, configmap.Namespace, err)
			}
			err = octx.ConfigContext().ApplyConfig(cfg, fmt.Sprintf("%s/%s",
				configmap.Namespace, configmap.Name))
			if err != nil {
				return fmt.Errorf("cannot apply ocm config in \"%s\" in namespace \"%s\": %w",
					configmap.Name, configmap.Namespace, err)
			}
		}
	}

	return nil
}

func RegexpFilter(regex string) (matcher.Matcher[string], error) {
	if regex == "" {
		return func(_ string) bool {
			return true
		}, nil
	}
	match, err := regexp.Compile(regex)
	if err != nil {
		return nil, err
	}

	return func(s string) bool {
		return match.MatchString(s)
	}, nil
}

func GetLatestValidVersion(_ context.Context, versions []string, semvers string, filter ...matcher.Matcher[string]) (*semver.Version, error) {
	constraint, err := semver.NewConstraint(semvers)
	if err != nil {
		return nil, err
	}

	var f matcher.Matcher[string]
	filtered := versions
	if len(filter) > 0 {
		f = filter[0]
		for _, version := range versions {
			if f(version) {
				filtered = append(filtered, version)
			}
		}
	}
	vers, err := semverutils.MatchVersionStrings(filtered, constraint)
	if err != nil {
		return nil, err
	}

	return vers[len(vers)-1], nil
}

func VerifyComponentVersion(ctx context.Context, cv ocm.ComponentVersionAccess, sigs []string) (*Descriptors, error) {
	logger := log.FromContext(ctx).WithName("signature-validation")

	if len(sigs) == 0 || cv == nil {
		return nil, nil
	}
	octx := cv.GetContext()

	// TODO: We should also consider the possibility that the user's component hierarchy spans multiple ocm
	//  repositories. Since these would have to be configured in the ocm config as resolvers (at least for now, while
	//  we do not provide a dedicated option in our crds), the ocm contexts resolvers should already cover this. So,
	//  without ever having tested this myself in the context of signing, I is how it should look like.
	resolver := resolvers.NewCompoundResolver(cv.Repository(), octx.GetResolver())
	opts := signing.NewOptions(
		signing.Resolver(resolver),
		// do we really want to verify the digests here? isn't it sufficient to verify the signatures since
		// the digest verification can and has to be done anyways by the resource controller?
		signing.VerifyDigests(),
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

func ListComponentDescriptors(_ context.Context, cv ocm.ComponentVersionAccess, r ocm.ComponentVersionResolver) (*Descriptors, error) {
	descriptors := &Descriptors{}
	_, err := utils.Walk(nil, cv, r,
		func(_ common.WalkingState[*compdesc.ComponentDescriptor, ocm.ComponentVersionAccess], cv ocm.ComponentVersionAccess) (bool, error) {
			descriptors.List = append(descriptors.List, cv.GetDescriptor())

			return true, nil
		})
	if err != nil {
		return nil, err
	}

	return descriptors, nil
}

// TODO: discuss whether latestcv should be able to also have a label that enforces downgradability

// IsDowngradable checks whether a component version (currentcv) is downgrabale to another component version (latestcv).
func IsDowngradable(_ context.Context, currentcv ocm.ComponentVersionAccess, latestcv ocm.ComponentVersionAccess) (bool, error) {
	data, ok := currentcv.GetDescriptor().GetLabels().Get(deliveryv1alpha1.OCMLabelDowngradable)
	if !ok {
		return false, nil
	}
	var vers string
	err := json.Unmarshal(data, &vers)
	if err != nil {
		return false, err
	}
	constaint, err := semver.NewConstraint(vers)
	if err != nil {
		return false, err
	}
	vers = latestcv.GetVersion()
	semvers, err := semver.NewVersion(vers)
	if err != nil {
		return false, err
	}

	downgradable := constaint.Check(semvers)

	return downgradable, nil
}

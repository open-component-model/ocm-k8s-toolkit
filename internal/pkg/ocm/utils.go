package ocm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/Masterminds/semver/v3"
	"github.com/mandelsoft/goutils/matcher"
	"github.com/mandelsoft/goutils/sliceutils"
	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"ocm.software/ocm/api/credentials/config"
	"ocm.software/ocm/api/credentials/extensions/repositories/dockerconfig"
	"ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/compdesc"
	"ocm.software/ocm/api/ocm/extensions/attrs/signingattr"
	utils "ocm.software/ocm/api/ocm/ocmutils"
	"ocm.software/ocm/api/ocm/tools/signing"
	common "ocm.software/ocm/api/utils/misc"
	"ocm.software/ocm/api/utils/runtime"
	"ocm.software/ocm/api/utils/semverutils"
	"regexp"
	"slices"
	"strings"
)

// TODO: This function should be almost entirely replaced by the ocm k8s secret
//  manager once it is ready!

// ConfigureContext reads all the secrets and config maps, checks them for
// known configuration types and applies them to the context.
func ConfigureContext(octx ocm.Context, obj *deliveryv1alpha1.Component, secrets []corev1.Secret, configmaps []corev1.ConfigMap, configset ...string) error {
	signinfo := signingattr.Get(octx)

	valueSigs := sliceutils.Filter(obj.Spec.Verify, func(signature deliveryv1alpha1.Verification) bool {
		return signature.Value != ""
	})
	secretSigs := sliceutils.Filter(obj.Spec.Verify, func(signature deliveryv1alpha1.Verification) bool {
		return signature.Value == "" && signature.SecretRef != ""
	})

	for _, sig := range valueSigs {
		value, err := base64.StdEncoding.DecodeString(sig.Value)
		if err != nil {
			return err
		}
		signinfo.RegisterPublicKey(sig.Signature, value)
	}

	for _, secret := range secrets {
		var signature string
		if slices.ContainsFunc(secretSigs, func(sig deliveryv1alpha1.Verification) bool {
			if sig.SecretRef == secret.Name {
				signature = sig.Signature
				return true
			}
			return false
		}) {
			if certBytes, ok := secret.Data[signature]; ok {
				signinfo.RegisterPublicKey(signature, certBytes)
			}
		}

		if dockerConfigBytes, ok := secret.Data[corev1.DockerConfigJsonKey]; ok {
			spec := dockerconfig.NewRepositorySpecForConfig(dockerConfigBytes, true)

			if _, err := octx.CredentialsContext().RepositoryForSpec(spec); err != nil {
				return fmt.Errorf("cannot create credentials from secret: %w", err)
			}
		}

		if ocmConfigBytes, ok := secret.Data[deliveryv1alpha1.OCMCredentialConfigKey]; ok {
			cfg, err := octx.ConfigContext().GetConfigForData(ocmConfigBytes, runtime.DefaultYAMLEncoding)
			if err != nil {
				return err
			}

			if cfg.GetKind() == config.ConfigType {
				if err := octx.ConfigContext().ApplyConfig(cfg, fmt.Sprintf("ocm config secret: %s/%s", secret.Namespace, secret.Name)); err != nil {
					return err
				}
			}
		}
	}

	for _, configmap := range configmaps {
		ocmConfigData, ok := configmap.Data[deliveryv1alpha1.OCMConfigKey]
		if !ok {
			return fmt.Errorf("ocm configuration config map does not contain key \"%s\"", deliveryv1alpha1.OCMConfigKey)
		}
		if len(ocmConfigData) > 0 {
			cfg, err := octx.ConfigContext().GetConfigForData([]byte(ocmConfigData), nil)
			if err != nil {
				return fmt.Errorf("invalid ocm config in \"%s\" in namespace \"%s\": %w", configmap.Name, configmap.Namespace, err)
			}
			err = octx.ConfigContext().ApplyConfig(cfg, fmt.Sprintf("%s/%s", configmap.Namespace, configmap.Name))
			if err != nil {
				return fmt.Errorf("cannot apply ocm config in \"%s\" in namespace \"%s\": %w", configmap.Name, configmap.Namespace, err)

			}
		}
	}
	var set string
	if len(configset) > 0 {
		set = configset[0]
	}
	if set != "" {
		err := octx.ConfigContext().ApplyConfigSet(set)
		if err != nil {
			return fmt.Errorf("cannot apply ocm config set %s: %w", *obj.Spec.ConfigSet, err)
		}
	}
	return nil
}

func RegexpFilter(regex string) (matcher.Matcher[string], error) {
	if regex == "" {
		return func(s string) bool {
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

func GetLatestValidVersion(component ocm.ComponentAccess, semvers string, filter ...matcher.Matcher[string]) (*semver.Version, error) {
	constraint, err := semver.NewConstraint(semvers)
	if err != nil {
		return nil, err
	}
	versions, err := component.ListVersions()
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

func VerifyComponentVersion(cv ocm.ComponentVersionAccess, sigs []string) ([]*compdesc.ComponentDescriptor, error) {
	if len(sigs) == 0 || cv == nil {
		return nil, nil
	}
	octx := cv.GetContext()
	logger := octx.Logger().WithName("signature-validation")

	// TODO: We should also consider the possibility that the user's component hierarchy spans multiple ocm
	//  repositories. Since these would have to be configured in the ocm config as resolvers (at least for now, while
	//  we do not provide a dedicated option in our crds), the ocm contexts resolvers should already cover this. So,
	//  without ever having tested this myself in the context of signing, I is how it should look like.
	resolver := ocm.NewCompoundResolver(cv.Repository(), octx.GetResolver())
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

	return signing.ListComponentDescriptors(ws), nil
}

func ListComponentDescriptors(cv ocm.ComponentVersionAccess, r ocm.ComponentVersionResolver) ([]*compdesc.ComponentDescriptor, error) {
	descriptors := []*compdesc.ComponentDescriptor{}
	_, err := utils.Walk[*compdesc.ComponentDescriptor](nil, cv, r,
		func(state common.WalkingState[*compdesc.ComponentDescriptor, ocm.ComponentVersionAccess], cv ocm.ComponentVersionAccess) (bool, error) {
			descriptors = append(descriptors, cv.GetDescriptor())
			return true, nil
		})
	if err != nil {
		return nil, err
	}
	return descriptors, nil
}

// TODO: discuss whether latestcv should be able to also have a label that enforces downgradability

// IsDowngradable checks whether a component version (currentcv) is downgrabale to another component version (latestcv).
func IsDowngradable(ctx context.Context, currentcv ocm.ComponentVersionAccess, latestcv ocm.ComponentVersionAccess) (bool, error) {
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

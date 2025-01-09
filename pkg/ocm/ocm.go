package ocm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/Masterminds/semver/v3"
	"github.com/mandelsoft/goutils/matcher"
	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"ocm.software/ocm/api/credentials/extensions/repositories/dockerconfig"
	"ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/compdesc"
	"ocm.software/ocm/api/ocm/cpi"
	"ocm.software/ocm/api/ocm/extensions/attrs/signingattr"
	utils "ocm.software/ocm/api/ocm/ocmutils"
	"ocm.software/ocm/api/ocm/tools/signing"
	common "ocm.software/ocm/api/utils/misc"
	"ocm.software/ocm/api/utils/runtime"
	"ocm.software/ocm/api/utils/semverutils"

	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
)

// ConfigureContext adds all the configuration data found in the config maps and
// secrets specified through the OCMConfiguration objects to the ocm context.
// NOTE: ConfigMaps and Secrets are slightly different, since secrets can also
// contain credentials in form of docker config jsons.
//
// Furthermore, it registers the public keys for the verification of signatures
// in the ocm context.
func ConfigureContext(ctx context.Context, octx ocm.Context, client ctrl.Client,
	configs []v1alpha1.OCMConfiguration, verifications ...[]Verification,
) error {
	for _, config := range configs {
		switch config.Ref.Kind {
		case "Secret":
			var secret corev1.Secret
			err := client.Get(ctx, ctrl.ObjectKey{
				Namespace: config.Ref.Namespace,
				Name:      config.Ref.Name,
			}, &secret)
			if err != nil {
				return fmt.Errorf("configure context cannot fetch secret "+
					"%s/%s: %w", config.Ref.Namespace, config.Ref.Name, err)
			}
			err = ConfigureContextForSecret(ctx, octx, &secret)
			if err != nil {
				return fmt.Errorf("configure context failed for secret "+
					"%s/%s: %w", config.Ref.Namespace, config.Ref.Name, err)
			}
		case "ConfigMap":
			var configmap corev1.ConfigMap
			err := client.Get(ctx, ctrl.ObjectKey{
				Namespace: config.Ref.Namespace,
				Name:      config.Ref.Name,
			}, &configmap)
			if err != nil {
				return fmt.Errorf("configure context cannot fetch config "+
					"map %s/%s: %w", config.Ref.Namespace, config.Ref.Name, err)
			}
			err = ConfigureContextForConfigMaps(ctx, octx, &configmap)
			if err != nil {
				return fmt.Errorf("configure context failed for secret "+
					"%s/%s: %w", config.Ref.Namespace, config.Ref.Name, err)
			}
		}
	}

	// If we were to introduce further functionality into the controller that
	// have to use the signing registry we retrieve from the context here
	// (e.g. signing), we would have to change the coding so that the signing
	// operation and the verification operation use dedicated signing stores.
	if len(verifications) > 0 {
		if len(verifications) > 1 {
			return fmt.Errorf("only one verification list is supported")
		}
		signinfo := signingattr.Get(octx)

		for _, verifi := range verifications[0] {
			signinfo.RegisterPublicKey(verifi.Signature, verifi.PublicKey)
		}
	}

	return nil
}

// ConfigureContextForSecret adds the ocm configuration data as well as
// credentials in the docker config json format found in the secret to the
// ocm context.
func ConfigureContextForSecret(_ context.Context, octx ocm.Context, secret *corev1.Secret) error {
	if dockerConfigBytes, ok := secret.Data[corev1.DockerConfigJsonKey]; ok {
		if len(dockerConfigBytes) > 0 {
			spec := dockerconfig.NewRepositorySpecForConfig(dockerConfigBytes, true)

			if _, err := octx.CredentialsContext().RepositoryForSpec(spec); err != nil {
				return fmt.Errorf("failed to apply credentials from docker"+
					"config json in secret %s/%s: %w", secret.Namespace, secret.Name, err)
			}
		}
	}

	if ocmConfigBytes, ok := secret.Data[v1alpha1.OCMConfigKey]; ok {
		if len(ocmConfigBytes) > 0 {
			cfg, err := octx.ConfigContext().GetConfigForData(ocmConfigBytes, runtime.DefaultYAMLEncoding)
			if err != nil {
				return fmt.Errorf("failed to deserialize ocm config data "+
					"in secret %s/%s: %w", secret.Namespace, secret.Name, err)
			}

			err = octx.ConfigContext().ApplyConfig(cfg, fmt.Sprintf("ocm config secret: %s/%s",
				secret.Namespace, secret.Name))
			if err != nil {
				return fmt.Errorf("failed to apply ocm config in secret "+
					"%s/%s: %w", secret.Namespace, secret.Name, err)
			}
		}
	}

	return nil
}

// ConfigureContextForConfigMaps adds the ocm configuration data found in the
// secret to the ocm context.
func ConfigureContextForConfigMaps(_ context.Context, octx ocm.Context, configmap *corev1.ConfigMap) error {
	ocmConfigData, ok := configmap.Data[v1alpha1.OCMConfigKey]
	if !ok {
		return fmt.Errorf("ocm configuration config map does not contain key \"%s\"",
			v1alpha1.OCMConfigKey)
	}
	if len(ocmConfigData) > 0 {
		cfg, err := octx.ConfigContext().GetConfigForData([]byte(ocmConfigData), nil)
		if err != nil {
			return fmt.Errorf("failed to deserialize ocm config data "+
				"in config map %s/%s: %w", configmap.Namespace, configmap.Name, err)
		}
		err = octx.ConfigContext().ApplyConfig(cfg, fmt.Sprintf("%s/%s",
			configmap.Namespace, configmap.Name))
		if err != nil {
			return fmt.Errorf("failed to apply ocm config in config map "+
				"%s/%s: %w", configmap.Namespace, configmap.Name, err)
		}
	}

	return nil
}

// GetEffectiveConfig returns the effective configuration for the given config
// ref provider object. Therefore, references to config maps and secrets (that
// are supposed to contain ocm configuration data) are directly returned.
// Furthermore, references to other ocm objects are resolved and their effective
// configuration (so again, config map and secret references) with policy
// propagate are returned.
func GetEffectiveConfig(ctx context.Context, client ctrl.Client, obj v1alpha1.ConfigRefProvider) ([]v1alpha1.OCMConfiguration, error) {
	configs := obj.GetSpecifiedOCMConfig()

	if len(configs) == 0 {
		return nil, nil
	}

	var refs []v1alpha1.OCMConfiguration
	for _, config := range configs {
		if config.Ref.Namespace == "" {
			config.Ref.Namespace = obj.GetNamespace()
		}

		var resource v1alpha1.ConfigRefProvider
		if config.Ref.Kind == "Secret" || config.Ref.Kind == "ConfigMap" {
			if config.Ref.APIVersion == "" {
				config.Ref.APIVersion = corev1.SchemeGroupVersion.String()
			}
			refs = append(refs, config)
		} else {
			if config.Ref.APIVersion == "" {
				return nil, fmt.Errorf("api version must be set for reference of kind %s", config.Ref.Kind)
			}

			switch config.Ref.Kind {
			case v1alpha1.KindOCMRepository:
				resource = &v1alpha1.OCMRepository{}
			case v1alpha1.KindComponent:
				resource = &v1alpha1.Component{}
			case v1alpha1.KindResource:
				resource = &v1alpha1.Resource{}
			default:
				return nil, fmt.Errorf("unsupported reference kind: %s", config.Ref.Kind)
			}

			if err := client.Get(ctx, ctrl.ObjectKey{Namespace: config.Ref.Namespace, Name: config.Ref.Name}, resource); err != nil || resource == nil {
				return nil, fmt.Errorf("failed to fetch resource %s: %w", config.Ref.Name, err)
			}

			for _, ref := range resource.GetPropagatedOCMConfig() {
				// do not propagate the policy of the parent resource but set
				// the policy specified in the respective config
				ref.Policy = config.Policy
				refs = append(refs, ref)
			}
		}
	}

	return refs, nil
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

// IsDowngradable checks whether a component version (currentcv) is downgrabale to another component version (latestcv).
func IsDowngradable(_ context.Context, currentcv ocm.ComponentVersionAccess, latestcv ocm.ComponentVersionAccess) (bool, error) {
	data, ok := currentcv.GetDescriptor().GetLabels().Get(v1alpha1.OCMLabelDowngradable)
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

type redirectedResourceAccess struct {
	cpi.ResourceAccess
	RedirectedAccessMethod cpi.AccessMethod
}

func (r *redirectedResourceAccess) AccessMethod() (cpi.AccessMethod, error) {
	return r.RedirectedAccessMethod, nil
}

func NewRedirectedResourceAccess(r cpi.ResourceAccess, bacc cpi.DataAccess) (cpi.ResourceAccess, error) {
	m, err := r.AccessMethod()
	if err != nil {
		return nil, err
	}
	rm := signing.NewRedirectedAccessMethod(m, bacc)

	return &redirectedResourceAccess{
		ResourceAccess:         r,
		RedirectedAccessMethod: rm,
	}, nil
}

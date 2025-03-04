package configuration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"ocm.software/ocm/api/ocm/ocmutils/localize"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/substitute"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/substitute/steps"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/util"
)

type RawConfig interface {
	UnpackIntoDirectory(path string) error
	Open() (io.ReadCloser, error)
}

type Config interface {
	GetRules() []v1alpha1.ConfigurationRule
}

type Target interface {
	UnpackIntoDirectory(path string) error
}

type Client interface {
	client.Reader
	// Scheme returns the scheme this client is using.
	Scheme() *runtime.Scheme
}

// Configure configures the target with the configuration.
// The target is configured in a temporary directory under basePath and the path of the directory is returned.
//
// The function returns an error if the configuration itself (the substitution) fails.
func Configure(ctx context.Context,
	clnt Client,
	rawConfig RawConfig,
	target Target,
	basePath string,
) (string, error) {
	logger := log.FromContext(ctx)
	targetDir := filepath.Join(basePath, "target")
	if err := target.UnpackIntoDirectory(targetDir); errors.Is(err, ociartifact.ErrAlreadyUnpacked) {
		logger.Info("target was already present, reusing existing directory", "path", targetDir)
	} else if err != nil {
		return "", fmt.Errorf("failed to get target directory: %w", err)
	}

	// TODO: Workaround because the tarball from artifact storer uses a folder
	//  named after the util name instead of storing at artifact root level as this is the expected format
	//  for helm tgz archives.
	//  See issue: https://github.com/helm/helm/issues/5552
	useSubDir, subDir, err := util.IsHelmChart(targetDir)
	if err != nil {
		return "", fmt.Errorf("failed to determine if target is a helm chart to traverse into subdirectory: %w", err)
	}
	if useSubDir {
		targetDir = subDir
	}

	cfg, err := ParseConfig(rawConfig, clnt.Scheme())
	if err != nil {
		return "", fmt.Errorf("failed to parse configuration: %w", err)
	}

	templateFunctions := sprig.FuncMap()
	maps.Copy(templateFunctions, util.KubernetesObjectReferenceTemplateFunc(ctx, clnt))

	substitutionSteps, err := substitutionStepsFromConfig(cfg, templateFunctions)
	if err != nil {
		return "", fmt.Errorf("failed to create substitution steps: %w", err)
	}

	engine, err := substitute.NewEngine(targetDir)
	if err != nil {
		return "", fmt.Errorf("failed to create substitution engine: %w", err)
	}

	engine.AddSteps(substitutionSteps...)

	if err := engine.Substitute(); err != nil {
		return "", fmt.Errorf("failed to substitute: %w", err)
	}

	if useSubDir {
		// if we are using a subdirectory (see above),
		// we need to direct the artifact path to the original directory again to return a proper localization
		return filepath.Dir(targetDir), nil
	}

	return targetDir, nil
}

func substitutionStepsFromConfig(cfg Config, templateFunctions template.FuncMap) ([]steps.Step, error) {
	var st []steps.Step

	rules := cfg.GetRules()
	substitutions := make(localize.Substitutions, 0, len(rules))
	for _, rule := range rules {
		if substitutionRule := rule.YAMLSubstitution; substitutionRule != nil {
			if err := substitutions.Add(
				"util-reference",
				substitutionRule.Target.File.Path,
				substitutionRule.Target.File.Value,
				substitutionRule.Source.Value,
			); err != nil {
				return nil, fmt.Errorf("failed to add resolved rule: %w", err)
			}

			continue
		}

		if goTemplate := rule.GoTemplate; goTemplate != nil {
			var data map[string]any
			if goTemplate.Data != nil {
				if err := json.Unmarshal(goTemplate.Data.Raw, &data); err != nil {
					return nil, fmt.Errorf("failed to unmarshal gotemplate data for transformation: %w", err)
				}
			}
			step := steps.NewGoTemplateBasedSubstitutionStep(
				rule.GoTemplate.FileTarget.Path,
				templateFunctions,
				data,
				&steps.Delimiters{
					Left:  goTemplate.Delimiters.Left,
					Right: goTemplate.Delimiters.Right,
				},
			)
			st = append(st, step)
		}
	}
	if len(substitutions) > 0 {
		st = append(st, steps.NewOCMPathBasedSubstitutionStep(substitutions))
	}

	return st, nil
}

func ParseConfig(source RawConfig, scheme *runtime.Scheme) (config Config, err error) {
	cfgReader, err := source.Open()
	defer func() {
		err = errors.Join(err, cfgReader.Close())
	}()

	return util.Parse[v1alpha1.ResourceConfig](cfgReader, serializer.NewCodecFactory(scheme).UniversalDeserializer())
}

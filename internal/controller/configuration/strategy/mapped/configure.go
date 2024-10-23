package mapped

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"ocm.software/ocm/api/ocm/ocmutils/localize"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"
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
	trgt Target,
	basePath string,
) (string, error) {
	target, err := prepareTargetDirInBasePath(ctx, basePath, trgt)
	if err != nil {
		return "", fmt.Errorf("failed to prepare target directory: %w", err)
	}

	cfg, err := ParseConfig(rawConfig, clnt.Scheme())
	if err != nil {
		return "", fmt.Errorf("failed to parse configuration: %w", err)
	}

	substitutionSteps, err := substitutionStepsFromConfig(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to create substitution steps: %w", err)
	}

	engine, err := substitute.NewEngine(target)
	if err != nil {
		return "", fmt.Errorf("failed to create substitution engine: %w", err)
	}

	engine.AddSteps(substitutionSteps...)

	if err := engine.Substitute(); err != nil {
		return "", fmt.Errorf("failed to substitute: %w", err)
	}

	return target, nil
}

func substitutionStepsFromConfig(cfg Config) ([]steps.Step, error) {
	rules := cfg.GetRules()
	substitutions := make(localize.Substitutions, 0, len(rules))
	for _, rule := range rules {
		if err := substitutions.Add(
			"util-reference",
			rule.Target.FileTarget.Path,
			rule.Target.FileTarget.Value,
			rule.Source.ValueSource,
		); err != nil {
			return nil, fmt.Errorf("failed to add resolved rule: %w", err)
		}
	}
	step := steps.NewOCMPathBasedSubstitutionStep(substitutions)

	return []steps.Step{step}, nil
}

func prepareTargetDirInBasePath(ctx context.Context, basePath string, trgt Target) (string, error) {
	logger := log.FromContext(ctx)
	targetDir := filepath.Join(basePath, "target")
	if err := trgt.UnpackIntoDirectory(targetDir); errors.Is(err, artifact.ErrAlreadyUnpacked) {
		logger.Info("target was already present, reusing existing directory", "path", targetDir)
	} else if err != nil {
		return "", fmt.Errorf("failed to get target directory: %w", err)
	}

	// TODO Workaround because the tarball from artifact storer uses a folder
	// named after the util name instead of storing at artifact root level as this is the expected format
	// for helm tgz archives.
	// See issue: https://github.com/helm/helm/issues/5552
	useSubDir, subDir, err := util.IsHelmChart(targetDir)
	if err != nil {
		return "", fmt.Errorf("failed to determine if target is a helm chart to traverse into subdirectory: %w", err)
	}
	if useSubDir {
		targetDir = subDir
	}

	if useSubDir {
		// if we are using a subdirectory (see above),
		// we need to direct the artifact path to the original directory again to return a proper localization
		return filepath.Dir(targetDir), nil
	}

	return targetDir, nil
}

func ParseConfig(source RawConfig, scheme *runtime.Scheme) (config Config, err error) {
	cfgReader, err := source.Open()
	defer func() {
		err = errors.Join(err, cfgReader.Close())
	}()

	return util.Parse[v1alpha1.ResourceConfig](cfgReader, serializer.NewCodecFactory(scheme).UniversalDeserializer())
}

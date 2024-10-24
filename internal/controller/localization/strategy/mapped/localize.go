package mapped

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
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"ocm.software/ocm/api/ocm/compdesc"
	ocmmetav1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/ocmutils/localize"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/artifact"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/substitute"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/substitute/steps"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/util"
)

type Config interface {
	UnpackIntoDirectory(path string) error
	Open() (io.ReadCloser, error)
}

type LocalizationConfig interface {
	GetRules() []v1alpha1.LocalizationRule
}

type Target interface {
	UnpackIntoDirectory(path string) error
	GetResource() *v1alpha1.Resource
}

type Client interface {
	client.Reader
	// Scheme returns the scheme this client is using.
	Scheme() *runtime.Scheme
}

// Localize localizes the target with the source mapping rules according to Config.
// The target is localized in a temporary directory under basePath and the path to the directory is returned.
//
// The function returns an error if the localization itself (the substitution) or the resolution of any reference fails.
func Localize(ctx context.Context,
	clnt Client,
	strg *storage.Storage,
	cfg Config,
	trgt Target,
	basePath string,
) (string, error) {
	logger := log.FromContext(ctx)

	targetDir := filepath.Join(basePath, "target")
	if err := trgt.UnpackIntoDirectory(targetDir); errors.Is(err, artifact.ErrAlreadyUnpacked) {
		logger.Info("target was already present, reusing existing directory", "path", targetDir)
	} else if err != nil {
		return "", fmt.Errorf("failed to Get target directory: %w", err)
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

	// based on the source, determine the localization rules / config for localization
	config, err := ParseLocalizationConfig(cfg, serializer.NewCodecFactory(clnt.Scheme()).UniversalDeserializer())
	if err != nil {
		return "", fmt.Errorf("failed to Get config: %w", err)
	}

	// now resolve the targetResource reference from the target, to "self-localize" the target with resources contained
	// in the component descriptor. This is fetching the necessary data to actually localize the target.
	//
	// TODO The component descriptor is fetched based on the artifact reference of the targetResource, but technically
	// we could also fetch it via the OCM library directly, however we would have to configure the OCM context for this.
	targetResource := trgt.GetResource()
	componentSet, componentDescriptor, err := ComponentDescriptorAndSetFromResource(ctx, clnt, strg, targetResource)
	if err != nil {
		return "", fmt.Errorf("failed to Get component descriptor and set: %w", err)
	}

	// now setup the substitution engine on our target directory
	engine, err := substitute.NewEngine(targetDir)
	if err != nil {
		return "", fmt.Errorf("failed to create substitution engine: %w", err)
	}

	// currently we only have one set of rules that we split into go template and other substitution rules
	// The order of execution is
	// 1. GoTemplate rules applied in order of occurrence as individual steps
	// 2. Substitution rules applied at the end as one step that goes over all files
	goTemplateRules := make([]*v1alpha1.LocalizationRuleGoTemplate, 0)
	substitutionRules := make([]*v1alpha1.LocalizationRuleMap, 0)

	for _, rule := range config.GetRules() {
		if rule.GoTemplate != nil {
			goTemplateRules = append(goTemplateRules, rule.GoTemplate)

			continue
		}
		if rule.Map != nil {
			substitutionRules = append(substitutionRules, rule.Map)
		}
	}

	if len(goTemplateRules) > 0 {
		funcs := template.FuncMap{}
		for _, fm := range []template.FuncMap{
			sprig.FuncMap(),
			OCMResourceReferenceTemplateFunc(targetResource, componentDescriptor, componentSet),
			util.KubernetesObjectReferenceTemplateFunc(ctx, clnt),
		} {
			maps.Copy(funcs, fm)
		}
		goTemplateSteps, err := GoTemplateSubstitutionStep(funcs, goTemplateRules)
		if err != nil {
			return "", fmt.Errorf("failed to transform go template rules into steps: %w", err)
		}
		engine.AddSteps(goTemplateSteps...)
	}

	if len(substitutionRules) > 0 {
		step, err := OCMPathSubstitutionStep(
			substitutionRules,
			targetResource,
			componentDescriptor,
			componentSet,
		)
		if err != nil {
			return "", fmt.Errorf("failed to transform substitution rules into step: %w", err)
		}
		engine.AddStep(step)
	}

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

func ComponentDescriptorAndSetFromResource(
	ctx context.Context,
	clnt client.Reader,
	strg *storage.Storage,
	targetResource *v1alpha1.Resource,
) (compdesc.ComponentVersionResolver, *compdesc.ComponentDescriptor, error) {
	component, err := util.Get[v1alpha1.Component](ctx, clnt, v1alpha1.ObjectKey{
		Name:      targetResource.Spec.ComponentRef.Name,
		Namespace: targetResource.Namespace,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to Get component: %w", err)
	}
	artifact, err := util.GetNamespaced[artifactv1.Artifact](ctx, clnt, component.Status.ArtifactRef, component.Namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to Get artifact: %w", err)
	}
	componentSet, err := ocm.GetComponentSetForArtifact(strg, artifact)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to Get component version set: %w", err)
	}
	componentDescriptor, err := componentSet.LookupComponentVersion(component.Spec.Component, component.Status.Component.Version)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to lookup component version: %w", err)
	}

	return componentSet, componentDescriptor, nil
}

// OCMPathSubstitutionStep creates a substitution step that substitutes based on the given
// localization rules. It processes rules at follows:
// 1. Use the Resource reference from rule.Config as base
// 2. Resolve it against the given component descriptor (with given resolver) into its actual value
// 3. Based on the Transformation.Type, extract the value from the resolved reference
// 4. Add the resolved value to the substitution list to be replaced.
func OCMPathSubstitutionStep(
	substitutionRules []*v1alpha1.LocalizationRuleMap,
	targetResource *v1alpha1.Resource,
	componentDescriptor *compdesc.ComponentDescriptor,
	resolver compdesc.ComponentVersionResolver,
) (steps.Step, error) {
	substitutions := make(localize.Substitutions, 0, len(substitutionRules))

	var extraID ocmmetav1.Identity
	if targetResource.Status.Resource != nil {
		extraID = targetResource.Status.Resource.ExtraIdentity
	}

	for _, rule := range substitutionRules {
		unresolved := unresolvedRefFromSource(rule.Resource.Name, extraID)
		resolved, err := resolveResourceReferenceFromComponentDescriptor(unresolved, componentDescriptor, resolver)
		if err != nil {
			return nil, fmt.Errorf("failed to Get targetResource reference from component descriptor based on rule: %w", err)
		}

		val, err := valueFromTransformation(resolved, rule.Transformation.Type)
		if err != nil {
			return nil, fmt.Errorf("failed to Get value for resolving a localization rule: %w", err)
		}

		if err := substitutions.Add(
			"util-reference",
			rule.FileTarget.Path,
			rule.FileTarget.Value,
			val,
		); err != nil {
			return nil, fmt.Errorf("failed to add resolved rule: %w", err)
		}
	}
	step := steps.NewOCMPathBasedSubstitutionStep(substitutions)

	return step, nil
}

// GoTemplateSubstitutionStep creates a substitution step that substitutes based on GoTemplates.
// It processes rules at follows:
// 1. Create a set of go template functions (see GoTemplateFuncs) that can be used in any template
// 2. Potentially add any additional data for the template to be filled with when specified in the rule
// 3. Create a step for each rule that parses each file from the util target path.
func GoTemplateSubstitutionStep(
	funcs template.FuncMap,
	goTemplateRules []*v1alpha1.LocalizationRuleGoTemplate,
) ([]steps.Step, error) {
	stepsFromRules := make([]steps.Step, 0, len(goTemplateRules))
	for _, rule := range goTemplateRules {
		var data map[string]any
		if rule.Data != nil {
			if err := json.Unmarshal(rule.Data.Raw, &data); err != nil {
				return nil, fmt.Errorf("failed to unmarshal gotemplate data for transformation: %w", err)
			}
		}

		step := steps.NewGoTemplateBasedSubstitutionStep(
			rule.FileTarget.Path,
			funcs,
			data,
			&steps.Delimiters{
				Left:  rule.Delimiters.Left,
				Right: rule.Delimiters.Right,
			},
		)
		stepsFromRules = append(stepsFromRules, step)
	}

	return stepsFromRules, nil
}

// OCMResourceReferenceTemplateFunc creates a template function map that can be used in a GoTemplate
// to resolve a util reference from a component descriptor.
// Example:
// {{ OCMResourceReference "image" "Registry" }}
// this looks up the util reference "my-util" in the component descriptor and returns the image reference
// with v1alpha1.TransformationTypeRegistry, e.g. "registry.example.com/my-image" would become "registry.example.com".
func OCMResourceReferenceTemplateFunc(
	contextualResource *v1alpha1.Resource,
	descriptor *compdesc.ComponentDescriptor,
	resolver compdesc.ComponentVersionResolver,
) template.FuncMap {
	var extraID ocmmetav1.Identity
	if contextualResource.Status.Resource != nil {
		extraID = contextualResource.Status.Resource.ExtraIdentity
	}

	return template.FuncMap{
		"OCMResourceReference": func(resource string, transformationType v1alpha1.TransformationType) (string, error) {
			unresolved := unresolvedRefFromSource(resource, extraID)
			resolved, err := resolveResourceReferenceFromComponentDescriptor(unresolved, descriptor, resolver)
			if err != nil {
				return "", fmt.Errorf("failed to Get util reference from component descriptor: %w", err)
			}

			return valueFromTransformation(resolved, transformationType)
		},
	}
}

package mapped

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/go-jose/go-jose/v4/json"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/projectionfs"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ocm.software/ocm/api/ocm/compdesc"
	"ocm.software/ocm/api/ocm/ocmutils/localize"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	localizationclient "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/client"
	loctypes "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/types"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
)

type Source interface {
	UnpackIntoDirectory(path string) error
	GetStrategy() v1alpha1.LocalizationStrategy
	Open() (io.ReadCloser, error)
}

type Target interface {
	UnpackIntoDirectory(path string) error
	GetResource() *v1alpha1.Resource
}

type Client interface {
	localizationclient.Client
}

// Localize localizes the target with the source mapping rules according to localization.Config.
// The target is localized in a temporary directory and the path to the directory is returned.
// The caller is responsible for cleaning up the directory.
//
// The function returns an error if the localization itself (the substitution) or the resolution of any reference fails.
func Localize(ctx context.Context,
	clnt Client,
	strg *storage.Storage,
	src Source,
	trgt Target,
) (string, error) {
	logger := log.FromContext(ctx)

	// DO NOT Defer remove this, it will be removed once it has been tarred outside the localization.
	// The contract of this function is to return the path to the directory where the localized target is stored.
	basePath, err := os.MkdirTemp("", "mapped-")
	if err != nil {
		return "", fmt.Errorf("tmp dir error: %w", err)
	}

	targetDir := filepath.Join(basePath, "target")
	if err := trgt.UnpackIntoDirectory(targetDir); errors.Is(err, loctypes.ErrAlreadyUnpacked) {
		logger.Info("target was already present, reusing existing directory", "path", targetDir)
	} else if err != nil {
		return "", fmt.Errorf("failed to get target directory: %w", err)
	}

	// based on the source, determine the localization rules / config for localization
	config, err := localizationConfigFromSource(src)
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}

	// now resolve the targetResource reference from the target, to "self-localize" the target with resources contained
	// in the component descriptor. This is fetching the necessary metadata to actually localize the target.
	//
	// TODO The component descriptor is fetched based on the artifact reference of the targetResource, but technically
	// we could also fetch it via the OCM library directly, however we would have to configure the OCM context for this.
	targetResource := trgt.GetResource()
	component, err := get[v1alpha1.Component](ctx, clnt, targetResource.Spec.ComponentRef)
	if err != nil {
		return "", fmt.Errorf("failed to get component: %w", err)
	}
	artifact, err := getNamespaced[artifactv1.Artifact](ctx, clnt, component.Status.ArtifactRef, component.Namespace)
	if err != nil {
		return "", fmt.Errorf("failed to get artifact: %w", err)
	}
	componentSet, err := ocm.ComponentSetFromLocalArtifact(strg, artifact)
	if err != nil {
		return "", fmt.Errorf("failed to get component version set: %w", err)
	}
	componentDescriptor, err := componentSet.LookupComponentVersion(component.Status.Component.Component, component.Status.Component.Version)
	if err != nil {
		return "", fmt.Errorf("failed to lookup component version: %w", err)
	}

	substitutions := make(localize.Substitutions, 0)
	tplRules := make([]v1alpha1.LocalizationRule, 0)

	for i, rule := range config.Spec.Rules {
		if rule.Transformation.Type == v1alpha1.TransformationTypeGoTemplate {
			tplRules = append(tplRules, rule)

			continue
		}

		unresolved := unresolvedRefFromSource(rule.Source.Resource.Name, targetResource.Spec.Resource.ExtraIdentity)
		resolved, err := resolveResourceReferenceFromComponentDescriptor(unresolved, componentDescriptor, componentSet)
		if err != nil {
			return "", fmt.Errorf("failed to get targetResource reference from component descriptor based on rule at index %d: %w", i, err)
		}

		if err := addResolvedRule(&substitutions, rule, resolved); err != nil {
			return "", fmt.Errorf("failed to add resolved rule: %w", err)
		}
	}

	if len(substitutions) == 0 && len(tplRules) == 0 {
		return "", fmt.Errorf("no substitutions were applied, most likely caused by an incorrect configuration")
	}

	if len(tplRules) > 0 {
		funcs := goTemplateFuncs(ctx, clnt, targetResource, componentDescriptor, componentSet)
		if err := executeGoTemplateTransformation(targetDir, tplRules, funcs); err != nil {
			return "", fmt.Errorf("failed to execute go template transformation: %w", err)
		}
	}

	if len(substitutions) > 0 {
		// now localize the target with the resolved rules
		// this will substitute the references from the rules in the target with the actual image references
		targetFs, err := projectionfs.New(osfs.New(), targetDir)
		if err != nil {
			return "", fmt.Errorf("failed to create target filesystem: %w", err)
		}
		if err := localize.Substitute(substitutions, targetFs); err != nil {
			return "", fmt.Errorf("failed to localize: %w", err)
		}
	}

	return targetDir, nil
}

func goTemplateFuncs(
	ctx context.Context,
	clnt Client,
	targetResource *v1alpha1.Resource,
	componentDescriptor *compdesc.ComponentDescriptor,
	componentSet *compdesc.ComponentVersionSet,
) template.FuncMap {
	return template.FuncMap{
		"OCMResourceReference": func(resource string, transformationType v1alpha1.TransformationType) (string, error) {
			unresolved := unresolvedRefFromSource(resource, targetResource.Spec.Resource.ExtraIdentity)
			resolved, err := resolveResourceReferenceFromComponentDescriptor(unresolved, componentDescriptor, componentSet)
			if err != nil {
				return "", fmt.Errorf("failed to get resource reference from component descriptor: %w", err)
			}

			return valueFromTransformation(resolved, transformationType)
		},
		"KubernetesObjectReference": func(namespace, name string) (string, error) {
			obj := &unstructured.Unstructured{}
			if err := clnt.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, obj); err != nil {
				return "", fmt.Errorf("failed to get object: %w", err)
			}
			data, err := json.Marshal(obj)
			if err != nil {
				return "", fmt.Errorf("failed to marshal object: %w", err)
			}

			return string(data), nil
		},
	}
}

func executeGoTemplateTransformation(base string, rules []v1alpha1.LocalizationRule, funcs template.FuncMap) (err error) {
	for _, rule := range rules {
		pathSpec := filepath.Join(base, rule.Target.FileTarget.Path)

		allFuncs := sprig.FuncMap()
		maps.Copy(allFuncs, funcs)

		tpl, err := template.New(filepath.Base(pathSpec)).Funcs(allFuncs).Delims("ocm{", "}").ParseFiles(pathSpec)
		if err != nil {
			return fmt.Errorf("failed to parse template: %w", err)
		}

		if len(tpl.Templates()) == 0 {
			return fmt.Errorf("no templates found in file %s", pathSpec)
		}

		var data map[string]any
		if rule.Transformation.GoTemplate != nil {
			if err := json.Unmarshal(rule.Transformation.GoTemplate.Data.Raw, &data); err != nil {
				return fmt.Errorf("failed to unmarshal data: %w", err)
			}
		}

		var buf bytes.Buffer
		if err := tpl.Execute(&buf, data); err != nil {
			return fmt.Errorf("failed to execute template: %w", err)
		}

		if err := os.WriteFile(pathSpec, buf.Bytes(), os.ModePerm); err != nil {
			return fmt.Errorf("failed to write template result back to file: %w", err)
		}
	}

	return nil
}

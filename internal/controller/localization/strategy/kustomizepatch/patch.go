package kustomizepatch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	generator "github.com/fluxcd/pkg/kustomize"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/resid"
	"sigs.k8s.io/yaml"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	loctypes "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/types"
)

const modeReadWriteUser = 0o600

type Source interface {
	UnpackIntoDirectory(path string) error
	GetStrategy() v1alpha1.LocalizationStrategy
}

type Target interface {
	UnpackIntoDirectory(path string) error
}

func Localize(ctx context.Context, src Source, trgt Target) (string, error) {
	logger := log.FromContext(ctx)

	// DO NOT Defer remove this, it will be removed once it has been tarred.
	basePath, err := os.MkdirTemp("", "kustomization-")
	if err != nil {
		return "", fmt.Errorf("tmp dir error: %w", err)
	}

	// Fetch the data instead.
	workDir := filepath.Join(basePath, "work")
	if err = os.MkdirAll(workDir, os.ModePerm|os.ModeDir); err != nil {
		return "", fmt.Errorf("failed to create work directory: %w", err)
	}

	targetDir := filepath.Join(workDir, "target")
	if err := trgt.UnpackIntoDirectory(targetDir); errors.Is(err, loctypes.ErrAlreadyUnpacked) {
		logger.Info("target was already unpacked, reusing existing directory", "path", targetDir)
	} else if err != nil {
		return "", fmt.Errorf("failed to get target directory: %w", err)
	}

	sourceDir := filepath.Join(workDir, "source")
	if err := src.UnpackIntoDirectory(sourceDir); errors.Is(err, loctypes.ErrAlreadyUnpacked) {
		logger.Info("source was already unpacked, reusing existing directory", "path", sourceDir)
	} else if err != nil {
		return "", fmt.Errorf("failed to get source directory: %w", err)
	}

	resources, err := (&recursiveFileChecker{base: targetDir}).recursiveFiles()
	if err != nil {
		return "", fmt.Errorf("failed to get target resources: %w", err)
	}
	for i, res := range resources {
		resources[i] = filepath.Join("target", res)
	}

	kus := types.Kustomization{
		TypeMeta: types.TypeMeta{
			APIVersion: types.KustomizationVersion,
			Kind:       types.KustomizationKind,
		},
		Resources: resources,
		Patches:   convertPatches(src.GetStrategy().Patches, "source"),
	}

	kustomizationYAML, err := yaml.Marshal(kus)
	if err != nil {
		return "", fmt.Errorf("failed to marshal kustomization: %w", err)
	}

	if err = os.WriteFile(filepath.Join(workDir, "kustomization.yaml"), kustomizationYAML, modeReadWriteUser); err != nil {
		return "", fmt.Errorf("failed to write kustomization file: %w", err)
	}

	res, err := generator.SecureBuild(basePath, workDir, false)
	if err != nil {
		return "", fmt.Errorf("failed to build kustomization: %w", err)
	}

	data, err := AsYamlStream(res)
	if err != nil {
		return "", fmt.Errorf("failed to convert resources to yaml stream: %w", err)
	}

	localizationFilePath := filepath.Join(basePath, "localized.yaml")
	if err = writeStreamToFile(data, localizationFilePath); err != nil {
		return "", fmt.Errorf("failed to write resources to file: %w", err)
	}

	if err := os.RemoveAll(workDir); err != nil {
		return "", fmt.Errorf("failed to remove work directory: %w", err)
	}

	return filepath.Dir(localizationFilePath), nil
}

func convertPatches(patches []v1alpha1.LocalizationKustomizePatch, relativePathAddition string) []types.Patch {
	converted := make([]types.Patch, 0, len(patches))
	for _, p := range patches {
		patch := types.Patch{
			Path:    filepath.Join(relativePathAddition, p.Path),
			Patch:   p.Patch,
			Target:  convertTarget(p.Target),
			Options: p.Options,
		}
		converted = append(converted, patch)
	}

	return converted
}

func convertTarget(target *v1alpha1.LocalizationSelector) *types.Selector {
	if target == nil {
		return nil
	}

	return &types.Selector{
		ResId: resid.ResId{
			Gvk:       resid.NewGvk(target.Group, target.Version, target.Kind),
			Name:      target.Name,
			Namespace: target.Namespace,
		},
		AnnotationSelector: target.AnnotationSelector,
		LabelSelector:      target.LabelSelector,
	}
}

func writeStreamToFile(data io.Reader, path string) (err error) {
	var outputFile *os.File
	if outputFile, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY, os.ModePerm); err != nil {
		return fmt.Errorf("failed to open output file: %w", err)
	}
	defer func() {
		err = errors.Join(err, outputFile.Close())
	}()

	if _, err = io.Copy(outputFile, data); err != nil {
		return fmt.Errorf("failed to write stream to file %s: %w", path, err)
	}

	return err
}

type recursiveFileChecker struct {
	base    string
	patches []string
}

func (pr *recursiveFileChecker) recursiveFiles() ([]string, error) {
	pr.patches = pr.patches[:0]
	if err := filepath.WalkDir(pr.base, pr.walk); err != nil {
		return nil, err
	}

	return pr.patches, nil
}

func (pr *recursiveFileChecker) walk(path string, d os.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if d.IsDir() {
		if path != pr.base {
			return pr.walk(path, d, err)
		}

		return nil
	}
	rel, err := filepath.Rel(pr.base, path)
	if err != nil {
		return err
	}
	pr.patches = append(pr.patches, rel)

	return nil
}

func AsYamlStream(resMap resmap.ResMap) (io.Reader, error) {
	firstObj := true
	var b []byte
	buf := bytes.NewBuffer(b)
	for _, res := range resMap.Resources() {
		out, err := res.AsYAML()
		if err != nil {
			return nil, fmt.Errorf("failed to convert resource to yaml: %w", err)
		}
		if firstObj {
			firstObj = false
		} else {
			if _, err = buf.WriteString("---\n"); err != nil {
				return nil, err
			}
		}
		if _, err = buf.Write(out); err != nil {
			return nil, err
		}
	}

	return buf, nil
}

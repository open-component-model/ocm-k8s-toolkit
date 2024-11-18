/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	. "github.com/onsi/ginkgo/v2" //nolint:golint,revive,stylecheck // ginkgo...
	"github.com/openfluxcd/artifact/test/utils"
)

// Run executes the provided command within this context.
func Run(cmd *exec.Cmd) ([]byte, error) {
	dir, _ := GetProjectDir()
	cmd.Dir = dir

	if err := os.Chdir(cmd.Dir); err != nil {
		fmt.Fprintf(GinkgoWriter, "chdir dir: %s\n", err)
	}

	cmd.Env = append(cmd.Env, os.Environ()...)
	cmd.Env = append(cmd.Env, "GO110MODULE=on")

	command := strings.Join(cmd.Args, " ")
	fmt.Fprintf(GinkgoWriter, "running: %s\n", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s failed with error: (%w) %s", command, err, string(output))
	}

	return output, nil
}

// GetNonEmptyLines converts given command output string into individual objects
// according to line breakers, and ignores the empty elements in it.
func GetNonEmptyLines(output string) []string {
	var res []string
	elements := strings.Split(output, "\n")
	for _, element := range elements {
		if element != "" {
			res = append(res, element)
		}
	}

	return res
}

// GetProjectDir will return the directory where the project is.
func GetProjectDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return wd, err
	}
	wd = strings.ReplaceAll(wd, "/test/e2e", "")

	return wd, nil
}

func DeployAndWaitForResource(manifestFilePath, waitingFor string) error {
	cmd := exec.Command("kubectl", "apply", "-f", manifestFilePath)
	if _, err := utils.Run(cmd); err != nil {
		return err
	}
	DeferCleanup(func() error {
		cmd = exec.Command("kubectl", "delete", "-f", manifestFilePath)
		_, err := utils.Run(cmd)

		return err
	})

	cmd = exec.Command("kubectl", "wait", "-f", manifestFilePath,
		"--for", waitingFor,
		"--timeout", "5m",
	)
	_, err := utils.Run(cmd)

	return err
}

func WaitForResource(query, namespace, waitingFor string) error {
	cmd := exec.Command("kubectl", "wait", query,
		"--for", waitingFor,
		"--namespace", namespace,
		"--timeout", "5m",
	)
	_, err := utils.Run(cmd)

	return err
}

// SanitizeRegistryURL returns the scheme and rest of the URL if a scheme was provided.
func SanitizeRegistryURL(registryURL string) (string, string) {
	if strings.Contains(registryURL, "://") {
		subStr := strings.SplitAfter(registryURL, "://")

		return subStr[0], subStr[1]
	}

	return "", registryURL
}

// PrepareOCMComponent creates an OCM component from a component-constructor file. The component-constructor file can
// contain go-template-logic and the respective key-value pairs can be by templateValues.
// After creating the OCM component, the component is transferred to imageRegistry.
func PrepareOCMComponent(ccPath, imageRegistry string, templateValues ...string) error {
	By("creating ocm component")
	tmpDir, err := os.MkdirTemp("", "")
	if err != nil {
		return fmt.Errorf("could not create temporary directory: %w", err)
	}
	DeferCleanup(func() error {
		return os.RemoveAll(tmpDir)
	})

	// The localization should replace the original value of the resource image reference with the new
	// image reference from our image registry.
	// In some test-scenarios an internal image registry inside the cluster is used to store the images.
	// If this is the case, the OCM component-constructor cannot hold a static registry-url. Therefore,
	// go-template is used to replace the registry-url.
	//   If the environment variable INTERNAL_IMAGE_REGISTRY_URL is present, its value will be used.
	//   If the environment variable is not present, the initial value from IMAGE_REGISTRY_URL will be used.
	ctfDir := filepath.Join(tmpDir, "ctf")
	cmdArgs := []string{
		"add",
		"componentversions",
		"--create",
		"--file", ctfDir,
		ccPath,
	}

	if len(templateValues) > 0 {
		// We could support more template-functionalities (see ocmcli), but it is not required yet.
		cmdArgs = append(cmdArgs, "--templater", "go")
		cmdArgs = append(cmdArgs, templateValues...)
	}

	cmd := exec.Command("ocm", cmdArgs...)
	_, err = utils.Run(cmd)
	if err != nil {
		return fmt.Errorf("could not create ocm component: %w", err)
	}

	cmd = exec.Command("ocm", "transfer", "ctf", "--overwrite", ctfDir, imageRegistry)
	_, err = utils.Run(cmd)
	if err != nil {
		return fmt.Errorf("could not transfer ocm component: %w", err)
	}

	return nil
}

// DeployOCMComponents is a helper function that deploys all relevant OCM component parts as well as the respective
// source-controller resource. It expects all manifests in the passed path. The image registry is required as the
// corresponding value must be templated.
func DeployOCMComponents(manifestPath, imageRegistry string) error {
	By("creating and validating the custom resource OCM repository")
	// In some test-scenarios an internal image registry inside the cluster is used to upload the components.
	// If this is the case, the OCM repository manifest cannot hold a static registry-url. Therefore,
	// go-template is used to replace the registry-url.
	//   If the environment variable INTERNAL_IMAGE_REGISTRY_URL is present, its value will be used.
	//   If the environment variable is not present, the initial value from IMAGE_REGISTRY_URL will be used.
	manifestOCMRepository := filepath.Join(manifestPath, "ocmrepository.yaml")
	manifestContent, err := os.ReadFile(manifestOCMRepository)
	if err != nil {
		return fmt.Errorf("could not read ocm repository manifest: %w", err)
	}

	tmplOCMRepository, err := template.New("manifest").Parse(string(manifestContent))
	if err != nil {
		return fmt.Errorf("could not parse ocm repository manifest: %w", err)
	}

	dataOCMRepository := map[string]string{
		"ImageRegistry": imageRegistry,
	}

	var result bytes.Buffer
	if err := tmplOCMRepository.Execute(&result, dataOCMRepository); err != nil {
		return fmt.Errorf("could not execute ocm repository manifest: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "")
	if err != nil {
		return fmt.Errorf("could not create temporary directory: %w", err)
	}
	DeferCleanup(func() error {
		return os.RemoveAll(tmpDir)
	})

	const perm = 0o644
	manifestOCMRepository = filepath.Join(tmpDir, "manifestOCMRespository.yaml")
	if err := os.WriteFile(manifestOCMRepository, result.Bytes(), perm); err != nil {
		return fmt.Errorf("could not write ocm repository manifest: %w", err)
	}
	if err := DeployAndWaitForResource(manifestOCMRepository, "condition=Ready"); err != nil {
		return fmt.Errorf("could not deploy ocm component: %w", err)
	}

	By("creating and validating the custom resource OCM component")
	manifestComponent := filepath.Join(manifestPath, "component.yaml")
	if err := DeployAndWaitForResource(manifestComponent, "condition=Ready"); err != nil {
		return fmt.Errorf("could not deploy ocm component: %w", err)
	}

	By("creating and validating the custom resource OCM resource")
	manifestResource := filepath.Join(manifestPath, "resource.yaml")
	if err := DeployAndWaitForResource(manifestResource, "condition=Ready"); err != nil {
		return fmt.Errorf("could not deploy ocm resource: %w", err)
	}

	By("creating and validating the custom resource localization resource")
	manifestLocalizationResource := filepath.Join(manifestPath, "localization-resource.yaml")
	if err := DeployAndWaitForResource(manifestLocalizationResource, "condition=Ready"); err != nil {
		return fmt.Errorf("could not deploy ocm localization resource: %w", err)
	}

	By("creating and validating the custom resource localized resource")
	manifestLocalizedResource := filepath.Join(manifestPath, "localized-resource.yaml")
	if err := DeployAndWaitForResource(manifestLocalizedResource, "condition=Ready"); err != nil {
		return fmt.Errorf("could not deploy ocm localized resource: %w", err)
	}

	By("creating and validating the custom resource for the release")
	manifestRelease := filepath.Join(manifestPath, "release.yaml")
	if err := DeployAndWaitForResource(manifestRelease, "condition=Ready"); err != nil {
		return fmt.Errorf("could not deploy release: %w", err)
	}

	return nil
}

func GetVerifyPodFieldFunc(labelSelector, fieldQuery, expect string) error {
	return func() error {
		cmd := exec.Command("kubectl", "get", "pod", "-l", labelSelector, "-o", fieldQuery)
		output, err := utils.Run(cmd)
		if err != nil {
			return fmt.Errorf("failed to get podinfo: %w", err)
		}

		podField := strings.ReplaceAll(string(output), "\"", "")

		if podField != expect {
			return fmt.Errorf("expected pod field: %s, got: %s", expect, podField)
		}

		return nil
	}()
}

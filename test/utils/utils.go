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
	"errors"
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
	cmd.Dir = os.Getenv("PROJECT_DIR")

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

// DeployAndWaitForResource takes a manifest file of a k8s resource and deploys it with "kubectl". Correspondingly,
// a DeferCleanup-handler is created that will delete the resource, when the test-suite ends.
// Additionally, "waitingFor" is a resource condition to check if the resource was deployed successfully.
// Example:
//
//	err := DeployAndWaitForResource("./pod.yaml", "condition=Ready")
func DeployAndWaitForResource(manifestFilePath, waitingFor, timeout string) error {
	err := DeployResource(manifestFilePath)
	if err != nil {
		return err
	}

	cmd := exec.Command("kubectl", "wait", "-f", manifestFilePath,
		"--for", waitingFor,
		"--timeout", timeout,
	)
	_, err = utils.Run(cmd)

	return err
}

// DeployAndWaitForResource takes a manifest file of a k8s resource and deploys it with "kubectl". Correspondingly,
// a DeferCleanup-handler is created that will delete the resource, when the test-suite ends.
// In contrast to "DeployAndWaitForResource", this function does not wait for a certain condition to be fulfilled.
func DeployResource(manifestFilePath string) error {
	cmd := exec.Command("kubectl", "apply", "-f", manifestFilePath)
	_, err := utils.Run(cmd)
	if err != nil {
		return err
	}
	DeferCleanup(func() error {
		cmd = exec.Command("kubectl", "delete", "-f", manifestFilePath)
		_, err := utils.Run(cmd)

		return err
	})

	return err
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

	// Note: The option '--overwrite' is necessary, when a digest of a resource is changed or unknown (which is the case
	// in our default test)
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
func DeployOCMComponents(manifestPath, imageRegistry, timeout string) error {
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
	if err := DeployAndWaitForResource(manifestOCMRepository, "condition=Ready", timeout); err != nil {
		return fmt.Errorf("could not deploy ocm component: %w", err)
	}

	By("creating and validating the custom resource OCM component")
	manifestComponent := filepath.Join(manifestPath, "component.yaml")
	if err := DeployAndWaitForResource(manifestComponent, "condition=Ready", timeout); err != nil {
		return fmt.Errorf("could not deploy ocm component: %w", err)
	}

	By("creating and validating the custom resource OCM resource")
	manifestResource := filepath.Join(manifestPath, "resource.yaml")
	if err := DeployAndWaitForResource(manifestResource, "condition=Ready", timeout); err != nil {
		return fmt.Errorf("could not deploy ocm resource: %w", err)
	}

	By("creating and validating the custom resource localization resource")
	manifestLocalizationResource := filepath.Join(manifestPath, "localization-resource.yaml")
	if err := DeployAndWaitForResource(manifestLocalizationResource, "condition=Ready", timeout); err != nil {
		return fmt.Errorf("could not deploy ocm localization resource: %w", err)
	}

	By("creating and validating the custom resource localized resource")
	manifestLocalizedResource := filepath.Join(manifestPath, "localized-resource.yaml")
	if err := DeployAndWaitForResource(manifestLocalizedResource, "condition=Ready", timeout); err != nil {
		return fmt.Errorf("could not deploy ocm localized resource: %w", err)
	}

	By("creating and validating the custom resource for the release")
	manifestRelease := filepath.Join(manifestPath, "release.yaml")
	if err := DeployAndWaitForResource(manifestRelease, "condition=Ready", timeout); err != nil {
		return fmt.Errorf("could not deploy release: %w", err)
	}

	return nil
}

// CheckOCMComponent executes the OCM CLI command 'ocm check cv' with the passed component reference.
// If credentials are required, the path to the OCM configuration file can be supplied as the second parameter.
// Options are optional. For possible values see:
// https://github.com/open-component-model/ocm/blob/main/docs/reference/ocm_check_componentversions.md
func CheckOCMComponent(componentReference, ocmConfigPath string, options ...string) error {
	c := []string{"ocm"}
	if len(ocmConfigPath) > 0 {
		c = append(c, "--config", ocmConfigPath)
	}
	c = append(c, "check", "cv")
	if len(options) > 0 {
		c = append(c, options[0:]...)
	}
	c = append(c, componentReference)

	cmd := exec.Command(c[0], c[1:]...) //nolint:gosec // The argument list is constructed right above.
	if _, err := utils.Run(cmd); err != nil {
		return err
	}

	return nil
}

func GetOCMResourceImageRef(componentReference, resourceName, ocmConfigPath string) (string, error) {
	const imgRef = "imageReference:"
	c := []string{"ocm"}
	if len(ocmConfigPath) > 0 {
		c = append(c, "--config", ocmConfigPath)
	}
	c = append(c, "get", "resources", componentReference, resourceName, "-oyaml", "|", "grep", imgRef)

	cmd := exec.Command(c[0], c[1:]...) //nolint:gosec // The argument list is constructed right above.
	outBytes, err := utils.Run(cmd)
	if err != nil {
		return "", err
	}

	output := string(outBytes)
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, imgRef) {
			return l[len(imgRef+" "):], nil
		}
	}

	return output, errors.New("'" + imgRef + "' not found in the command output")
}

// GetVerifyPodFieldFunc is a helper function to return a function which checks for a pod with the passed label
// selector. It returns the result from a comparison of the query on a specified pod-field with the expected string.
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

// Create Kubernetes namespace.
func CreateNamespace(ns string) error {
	cmd := exec.Command("kubectl", "create", "ns", ns)
	_, err := utils.Run(cmd)

	return err
}

// Delete Kubernetes namespace.
func DeleteNamespace(ns string) error {
	cmd := exec.Command("kubectl", "delete", "ns", ns)
	_, err := utils.Run(cmd)

	return err
}

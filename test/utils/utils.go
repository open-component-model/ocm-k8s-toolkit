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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2" //nolint:revive // ginkgo...
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

	return WaitForResource(waitingFor, timeout, "-f", manifestFilePath)
}

// DeployResource takes a manifest file of a k8s resource and deploys it with "kubectl". Correspondingly,
// a DeferCleanup-handler is created that will delete the resource, when the test-suite ends.
// In contrast to "DeployAndWaitForResource", this function does not wait for a certain condition to be fulfilled.
func DeployResource(manifestFilePath string) error {
	cmd := exec.Command("kubectl", "apply", "-f", manifestFilePath)
	_, err := Run(cmd)
	if err != nil {
		return err
	}
	DeferCleanup(func() error {
		cmd = exec.Command("kubectl", "delete", "-f", manifestFilePath)
		_, err := Run(cmd)

		return err
	})

	return err
}

func WaitForResource(condition, timeout string, resource ...string) error {
	cmdArgs := append([]string{"wait", "--for=" + condition}, resource...)
	cmdArgs = append(cmdArgs, "--timeout="+timeout)
	cmd := exec.Command("kubectl", cmdArgs...)
	_, err := Run(cmd)

	return err
}

// PrepareOCMComponent creates an OCM component from a component-constructor file.
// After creating the OCM component, the component is transferred to imageRegistry.
func PrepareOCMComponent(name, componentConstructorPath, imageRegistry, signingKey string) error {
	By("creating ocm component for " + name)
	tmpDir := GinkgoT().TempDir()

	ctfDir := filepath.Join(tmpDir, "ctf")
	cmdArgs := []string{
		"add",
		"componentversions",
		"--create",
		"--file", ctfDir,
		componentConstructorPath,
	}

	cmd := exec.Command("ocm", cmdArgs...)
	_, err := Run(cmd)
	if err != nil {
		return fmt.Errorf("could not create ocm component: %w", err)
	}

	if signingKey != "" {
		By("signing ocm component for " + name)
		cmd = exec.Command(
			"ocm",
			"sign",
			"componentversions",
			"--signature",
			"ocm.software",
			"--private-key",
			signingKey,
			ctfDir,
		)
		_, err := Run(cmd)
		if err != nil {
			return fmt.Errorf("could not create ocm component: %w", err)
		}
	}

	By("transferring ocm component for " + name)
	// Note: The option '--overwrite' is necessary, when a digest of a resource is changed or unknown (which is the case
	// in our default test)
	cmdArgs = []string{
		"transfer",
		"ctf",
		"--overwrite",
		"--enforce",
		"--copy-resources",
		"--omit-access-types",
		"gitHub",
		ctfDir,
		imageRegistry,
	}

	cmd = exec.Command("ocm", cmdArgs...)
	_, err = Run(cmd)
	if err != nil {
		return fmt.Errorf("could not transfer ocm component: %w", err)
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
	if _, err := Run(cmd); err != nil {
		return err
	}

	return nil
}

// GetOCMResourceImageRef returns the image reference of a specified resource of a component version.
// For the format of component reference see OCM CLI documentation.
func GetOCMResourceImageRef(componentReference, resourceName, ocmConfigPath string) (string, error) {
	// Construct the command 'ocm get resources', which is used here to get the image reference of a resource.
	// See also: https://github.com/open-component-model/ocm/blob/main/docs/reference/ocm_get_resources.md
	c := []string{"ocm", "--loglevel", "error"}
	if len(ocmConfigPath) > 0 {
		c = append(c, "--config", ocmConfigPath)
	}
	c = append(c, "get", "resources", componentReference, resourceName, "-oJSON") // -oJSON is used to get the output in JSON format.

	cmd := exec.Command(c[0], c[1:]...) //nolint:gosec // The argument list is constructed right above.
	output, err := Run(cmd)
	if err != nil {
		return "", err
	}

	// This struct corresponds to the json format of the command output.
	// We are only interested in one specific field, the image reference. All other fields are omitted.
	type Result struct {
		Items []struct {
			Element struct {
				Access struct {
					ImageReference string `json:"imageReference"`
				} `json:"access"`
			} `json:"element"`
		} `json:"items"`
	}

	var r Result
	err = json.Unmarshal(output, &r)
	if err != nil {
		return "", errors.New("could not unmarshal command output: " + string(output))
	}
	if len(r.Items) != 1 {
		return "", errors.New("exactly one item is expected in command output: " + string(output))
	}

	return r.Items[0].Element.Access.ImageReference, nil
}

// Create Kubernetes namespace.
func CreateNamespace(ns string) error {
	cmd := exec.Command("kubectl", "create", "ns", ns)
	_, err := Run(cmd)

	return err
}

// Delete Kubernetes namespace.
func DeleteNamespace(ns string) error {
	cmd := exec.Command("kubectl", "delete", "ns", ns)
	_, err := Run(cmd)

	return err
}

// CompareResourceField compares the value of a specific field in a Kubernetes resource
// with an expected value.
//
// Parameters:
// - resource: The Kubernetes resource to query (e.g., "pod my-pod").
// - fieldSelector: A JSONPath expression to select the field to compare.
// - expected: The expected value of the field.
//
// Returns:
// - An error if the field value does not match the expected value or if the command fails.
func CompareResourceField(resource, fieldSelector, expected string) error {
	args := []string{"get"}
	args = append(args, strings.Split(resource, " ")...)
	args = append(args, "-o", "jsonpath="+fieldSelector)
	cmd := exec.Command("kubectl", args...)
	output, err := Run(cmd)
	if err != nil {
		return err
	}

	// Sanitize output
	result := strings.TrimSpace(string(output))
	result = strings.ReplaceAll(result, "'", "")

	if strings.TrimSpace(result) != expected {
		return fmt.Errorf("expected %s, got %s", expected, string(output))
	}

	return nil
}

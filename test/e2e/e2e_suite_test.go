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

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/open-component-model/ocm-k8s-toolkit/test/utils"
)

const namespace = "ocm-k8s-toolkit-system"

// Run e2e tests using the Ginkgo runner.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	fmt.Fprintf(GinkgoWriter, "Starting ocm-k8s-toolkit suite\n")

	BeforeSuite(func(ctx SpecContext) {
		By("Creating manager namespace", func() {
			cmd := exec.Command("kubectl", "create", "ns", namespace)
			_, _ = utils.Run(cmd)
			DeferCleanup(func() {
				By("Deleting manager namespace")
				cmd := exec.Command("kubectl", "delete", "ns", namespace)
				_, _ = utils.Run(cmd)
			})
		})

		By("Checking for an image registry", func() {
			imageRegistryRaw := os.Getenv("IMAGE_REGISTRY_URL")
			Expect(imageRegistryRaw).NotTo(BeEmpty())

			scheme, imageRegistry = utils.SanitizeRegistryURL(imageRegistryRaw)
		})

		By("Checking for an internal image registry", func() {
			// If an internal image registry in the kubernetes-cluster is used, the registry-url for the ocm repository must be adjusted
			// (see deployment of OCM repository below)
			imageRegistryRaw := os.Getenv("INTERNAL_IMAGE_REGISTRY_URL")
			if imageRegistryRaw == "" {
				imageRegistryRaw = imageRegistry
			}
			internalScheme, internalImageRegistry = utils.SanitizeRegistryURL(imageRegistryRaw)
		})

		// TODO: Replace image with demo-CV image (see https://github.com/open-component-model/ocm-project/issues/317)
		By("Providing referenced images to the image registry", func() {
			// Provide referenced images to our image registry to test localization
			imageRef = "podinfo:6.7.1"
			Expect(crane.Copy("ghcr.io/stefanprodan/podinfo:6.7.1", fmt.Sprintf("%s/%s", imageRegistry, imageRef))).To(Succeed())
		})

		By("Starting the operator", func() {
			// projectimage stores the name of the image used in the example
			var projectimage = imageRegistry + "/ocm.software/ocm-controller:v0.0.1"

			By("Building the manager(Operator) image")
			cmd := exec.Command("make", "docker-build")
			cmd.Env = []string{"IMG=" + projectimage}
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			cmd = exec.Command("make", "docker-push")
			cmd.Env = []string{"IMG=" + projectimage}
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("Installing CRDs")
			cmd = exec.Command("make", "install")
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			DeferCleanup(func() error {
				By("uninstalling CRDs")
				cmd = exec.Command("make", "uninstall")
				// In case ´make undeploy` already uninstalled the CRDs
				cmd.Env = append(os.Environ(), "IGNORE_NOT_FOUND=true")
				_, err = utils.Run(cmd)

				return err
			})

			// TODO: Behaviour with external CRDs depend on decision/solution for https://github.com/open-component-model/ocm-project/issues/327
			//By("Installing external CRDs")
			//cmd = exec.Command("kubectl", "apply", "--server-side", "-k", "https://github.com/openfluxcd/artifact//config/crd?ref=v0.1.1")
			//_, err = utils.Run(cmd)
			//ExpectWithOffset(1, err).NotTo(HaveOccurred())
			//DeferCleanup(func() error {
			//	By("Uninstalling external CRDs")
			//	cmd = exec.Command("kubectl", "delete", "-k", "https://github.com/openfluxcd/artifact//config/crd?ref=v0.1.1")
			//	_, err = utils.Run(cmd)

			//	return err
			//})

			By("Deploying the controller-manager")
			cmd = exec.Command("make", "deploy")
			cmd.Env = []string{"IMG=" + projectimage}
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			DeferCleanup(func() error {
				By("Un-deploying the controller-manager")
				cmd = exec.Command("make", "undeploy")
				cmd.Env = []string{"IMG=" + projectimage}
				_, err = utils.Run(cmd)

				return err
			})

			By("Validating that the controller-manager pod is running as expected")
			verifyControllerUp := func() error {
				// Get pod name

				cmd = exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				podNames := utils.GetNonEmptyLines(string(podOutput))
				if len(podNames) != 1 {
					return fmt.Errorf("expect 1 controller pods running, but got %d", len(podNames))
				}
				controllerPodName := podNames[0]
				ExpectWithOffset(2, controllerPodName).Should(ContainSubstring("controller-manager"))

				// Validate pod status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				status, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				if string(status) != "Running" {
					return fmt.Errorf("controller pod in %s status", status)
				}
				return nil
			}

			EventuallyWithOffset(1, verifyControllerUp, time.Minute, time.Second).Should(Succeed())
		})
	})

	RunSpecs(t, "e2e suite")
}

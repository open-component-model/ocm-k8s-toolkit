//go:build e2e

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
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/open-component-model/ocm-k8s-toolkit/test/utils"
)

const namespace = "ocm-k8s-toolkit-system"

var (
	imageRegistry         string
	internalImageRegistry string
)

var _ = Describe("controller", Ordered, func() {
	BeforeAll(func() {
		By("installing prometheus operator")
		Expect(utils.InstallPrometheusOperator()).To(Succeed())
		DeferCleanup(func() {
			By("uninstalling the prometheus operator")
			utils.UninstallPrometheusOperator()
		})

		By("installing the cert-manager")
		Expect(utils.InstallCertManager()).To(Succeed())
		DeferCleanup(func() {
			By("uninstalling the cert-manager bundle")
			utils.UninstallCertManager()
		})

		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, _ = utils.Run(cmd)
		DeferCleanup(func() {
			By("removing manager namespace")
			cmd := exec.Command("kubectl", "delete", "ns", namespace)
			_, _ = utils.Run(cmd)
		})

		By("checking for an image registry")
		imageRegistry = os.Getenv("IMAGE_REGISTRY_URL")
		Expect(imageRegistry).NotTo(BeEmpty())

		By("checking for an internal image registry")
		// If an internal image registry in the kubernetes-cluster is used, the registry-url for the ocm repository must be adjusted
		// (see deployment of OCM repository below)
		internalImageRegistry = os.Getenv("INTERNAL_IMAGE_REGISTRY_URL")
		if internalImageRegistry == "" {
			internalImageRegistry = imageRegistry
		}
	})

	Context("Operator", func() {
		It("should run successfully", func() {
			var controllerPodName string
			var err error

			// projectimage stores the name of the image used in the example
			// Note: If working with insecure registries it is required to use the scheme 'http://' for ocm. However,
			// docker does not like it. Thus, it is removed if present.
			var projectimage = strings.TrimLeft(imageRegistry, "http://") + "/ocm.software/ocm-controller:v0.0.1"

			By("building the manager(Operator) image")
			cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectimage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			cmd = exec.Command("make", "docker-push", fmt.Sprintf("IMG=%s", projectimage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("installing CRDs")
			cmd = exec.Command("make", "install")
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("deploying the controller-manager")
			cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectimage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("validating that the controller-manager pod is running as expected")
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
				controllerPodName = podNames[0]
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

		It("should deploy a helm resource", func() {
			By("checking for the helm controller")
			Expect(utils.WaitForResource("deployment.apps/helm-controller", "helm-system", "condition=Available")).To(Succeed())

			By("creating ocm component")
			tmpDir, err := os.MkdirTemp("", "")
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			DeferCleanup(func() error {
				return os.RemoveAll(tmpDir)
			})
			ctfDir := filepath.Join(tmpDir, "ctf-helm")
			Expect(utils.CreateOCMComponent("test/e2e/testdata/helm-release/component-constructor.yaml", ctfDir)).To(Succeed())
			Expect(utils.TransferOCMComponent(ctfDir, imageRegistry)).To(Succeed())

			By("creating the custom resource OCM repository")
			// In some test-scenarios an internal image registry inside the cluster is used to upload the components.
			// If this is the case, the OCM repository manifest cannot hold a static registry-url. Therefore,
			// go-template is used to replace the registry-url.
			//   If the environment variable INTERNAL_IMAGE_REGISTRY_URL is present, its value will be used.
			//   If the environment variable is not present, the initial value from IMAGE_REGISTRY_URL will be used.
			manifestOCMRepository := "test/e2e/testdata/helm-release/helm-ocmrepository.yaml"
			manifestContent, err := os.ReadFile(manifestOCMRepository)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			tmpl, err := template.New("manifest").Parse(string(manifestContent))
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			data := map[string]string{
				"ImageRegistry": internalImageRegistry,
			}

			var result bytes.Buffer
			Expect(tmpl.Execute(&result, data)).To(Succeed())

			manifestOCMRepository = filepath.Join(tmpDir, "manifestOCMRespository.yaml")
			Expect(os.WriteFile(manifestOCMRepository, result.Bytes(), 0644)).To(Succeed())
			Expect(utils.DeployAndWaitForResource(manifestOCMRepository, "condition=Ready")).To(Succeed())

			By("validating that the custom resource OCM repository was processed")
			Expect(utils.WaitForResource("ocmrepositories/helm-ocmrepository", "default", "condition=ready=true")).To(Succeed())

			By("creating and validating the custom resource OCM component")
			manifestComponent := "test/e2e/testdata/helm-release/helm-component.yaml"
			Expect(utils.DeployAndWaitForResource(manifestComponent, "condition=Ready")).To(Succeed())

			By("creating and validating the custom resource OCM resource")
			manifestResource := "test/e2e/testdata/helm-release/helm-resource.yaml"
			Expect(utils.DeployAndWaitForResource(manifestResource, "condition=Ready")).To(Succeed())

			// TODO
			By("creating the custom resource localized resource")
			By("validating that the custom resource localized resource was processed")

			// TODO
			By("creating the custom resource configured resource")
			By("validating that the custom resource configured resource was processed")

			By("creating the custom resource helm flux resource")
			manifestHelmRelease := "test/e2e/testdata/helm-release/helm-release.yaml"
			Expect(utils.DeployAndWaitForResource(manifestHelmRelease, "condition=Ready")).To(Succeed())

			By("validating that the custom resource helm flux resource was processed")
			Expect(utils.WaitForResource("helmreleases/helm-flux", "default", "condition=ready=true")).To(Succeed())

			By("validating that the resource was deployed successfully through the helm-controller")
			Expect(utils.WaitForResource("deployment.apps/helm-flux-podinfo", "default", "condition=Available")).To(Succeed())
		})
	})
})

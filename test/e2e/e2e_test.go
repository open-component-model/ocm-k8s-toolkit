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
	"path/filepath"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/open-component-model/ocm-k8s-toolkit/test/utils"
)

const namespace = "ocm-k8s-toolkit-system"

var (
	imageRegistry         string
	scheme                string
	internalImageRegistry string
	internalScheme        string
	imageRef              string
)

var _ = Describe("controller", Ordered, func() {
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, _ = utils.Run(cmd)
		DeferCleanup(func() {
			By("removing manager namespace")
			cmd := exec.Command("kubectl", "delete", "ns", namespace)
			_, _ = utils.Run(cmd)
		})

		By("checking for an image registry")
		imageRegistryRaw := os.Getenv("IMAGE_REGISTRY_URL")
		Expect(imageRegistryRaw).NotTo(BeEmpty())

		scheme, imageRegistry = utils.SanitizeRegistryURL(imageRegistryRaw)

		By("checking for an internal image registry")
		// If an internal image registry in the kubernetes-cluster is used, the registry-url for the ocm repository must be adjusted
		// (see deployment of OCM repository below)
		internalImageRegistryRaw := os.Getenv("INTERNAL_IMAGE_REGISTRY_URL")
		if internalImageRegistryRaw == "" {
			internalImageRegistryRaw = imageRegistry
		}
		internalScheme, internalImageRegistry = utils.SanitizeRegistryURL(internalImageRegistryRaw)

		// TODO: This is to specific and depending on the helmchart itself
		// Provide referenced images to our image registry to test localization
		By("providing referenced images to the image registry")
		imageRef = "podinfo:6.7.1"
		Expect(crane.Copy("ghcr.io/stefanprodan/podinfo:6.7.1", fmt.Sprintf("%s/%s", imageRegistry, imageRef))).To(Succeed())
	})

	Context("Operator", func() {
		It("should run successfully", func() {
			var controllerPodName string
			var err error

			// projectimage stores the name of the image used in the example
			// Note: If working with insecure registries it is required to use the scheme 'http://' for ocm. However,
			// docker does not like it. Thus, it is removed if present.
			var projectimage = imageRegistry + "/ocm.software/ocm-controller:v0.0.1"

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

			By("installing external CRDs")
			cmd = exec.Command("kubectl", "apply", "--server-side", "-k", "https://github.com/openfluxcd/artifact//config/crd?ref=v0.1.1")
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
			// Note: Namespace is taken from helm-controller default kustomization
			Expect(utils.WaitForResource("deployment.apps/helm-controller", "helm-system", "condition=Available")).To(Succeed())

			pd, err := utils.GetProjectDir()
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			Expect(utils.ConfigureAndDeployResources(
				filepath.Join(pd, "test/e2e/testdata/helm-release"),
				filepath.Join(internalImageRegistry, imageRef),
				scheme+imageRegistry,
				internalScheme+internalImageRegistry),
			).To(Succeed())

			By("validating that the resource was deployed successfully through the helm-controller")
			Expect(utils.WaitForResource("deployment.apps/helm-flux-podinfo", "default", "condition=Available")).To(Succeed())

			By("validating that the localization was successful")
			verifyFunc := utils.GetVerifyPodFieldFunc("app.kubernetes.io/name=helm-flux-podinfo", "jsonpath=\"{.items[0].spec.containers[?(@.name=='podinfo')].image}\"", filepath.Join(internalImageRegistry, imageRef))
			EventuallyWithOffset(1, verifyFunc, time.Minute, time.Second).Should(Succeed())
		})

		It("should deploy a kustomize resource", func() {
			By("checking for the kustomize controller")
			// Note: Namespace is taken from helm-controller default kustomization
			Expect(utils.WaitForResource("deployment.apps/kustomize-controller", "kustomize-system", "condition=Available")).To(Succeed())

			pd, err := utils.GetProjectDir()
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			Expect(utils.ConfigureAndDeployResources(
				filepath.Join(pd, "test/e2e/testdata/kustomize-release"),
				filepath.Join(internalImageRegistry, imageRef),
				scheme+imageRegistry,
				internalScheme+internalImageRegistry),
			).To(Succeed())

			By("validating that the resource was deployed successfully through the kustomize-controller")
			Expect(utils.WaitForResource("deployment.apps/kustomize-podinfo", "default", "condition=Available")).To(Succeed())

			By("validating that the localization was successful")
			verifyFunc := utils.GetVerifyPodFieldFunc("app=kustomize-podinfo", "jsonpath=\"{.items[0].spec.containers[?(@.name=='app')].image}\"", filepath.Join(internalImageRegistry, imageRef))
			EventuallyWithOffset(1, verifyFunc, time.Minute, time.Second).Should(Succeed())
		})
	})
})

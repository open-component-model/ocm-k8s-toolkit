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
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/open-component-model/ocm-k8s-toolkit/test/utils"
)

const ()

var (
	helmChart                   string
	kustomizationPath           string
	imageReference              string
	imageReferenceShort         string
	internalImageReference      string
	internalImageReferenceNoTag string
	imageRegistry               string
	scheme                      string
	internalImageRegistry       string
	internalScheme              string
)

var _ = Describe("controller", func() {
	Context("Operator", func() {
		It("should deploy a helm resource", func() {
			By("checking for the helm controller")
			// Note: Namespace is taken from helm-controller default kustomization
			cmd := exec.Command("kubectl", "wait", "deployment.apps/helm-controller",
				"--for", "condition=Available",
				"--namespace", "helm-system",
				"--timeout", "1m",
			)
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			pd, err := utils.GetProjectDir()
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			testData := filepath.Join(pd, "test/e2e/testdata/helm-release")

			Expect(utils.PrepareOCMComponent(
				filepath.Join(testData, "component-constructor.yaml"),
				scheme+imageRegistry,
				"HelmChart="+helmChart,
				"LocalizationConfigPath="+filepath.Join(testData, "localization-config.yaml"),
				"ImageReference="+internalImageReferenceNoTag, // Image without tag/identifier
			)).To(Succeed())

			Expect(utils.DeployOCMComponents(testData, internalScheme+internalImageRegistry)).To(Succeed())

			By("validating that the resource was deployed successfully through the helm-controller")
			cmd = exec.Command("kubectl", "wait", "deployment.apps/helm-flux-podinfo",
				"--for", "condition=Available",
				"--namespace", "default",
				"--timeout", "1m",
			)
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("validating that the localization was successful")
			verifyFunc := utils.GetVerifyPodFieldFunc("app.kubernetes.io/name=helm-flux-podinfo", "jsonpath=\"{.items[0].spec.containers[?(@.name=='podinfo')].image}\"", internalImageReference)
			EventuallyWithOffset(1, verifyFunc, time.Minute, time.Second).Should(Succeed())
		})

		It("should deploy a kustomize resource", func() {
			By("checking for the kustomize controller")
			// Note: Namespace is taken from helm-controller default kustomization
			cmd := exec.Command("kubectl", "wait", "deployment.apps/kustomize-controller",
				"--for", "condition=Available",
				"--namespace", "kustomize-system",
				"--timeout", "1m",
			)
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			pd, err := utils.GetProjectDir()
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			testData := filepath.Join(pd, "test/e2e/testdata/kustomize-release")

			Expect(utils.PrepareOCMComponent(
				filepath.Join(testData, "component-constructor.yaml"),
				scheme+imageRegistry,
				"KustomizationPath="+kustomizationPath,
				"LocalizationConfigPath="+filepath.Join(testData, "localization-config.yaml"),
				"ImageReference="+internalImageReference,
			)).To(Succeed())

			Expect(utils.DeployOCMComponents(testData, internalScheme+internalImageRegistry)).To(Succeed())

			By("validating that the resource was deployed successfully through the kustomize-controller")
			cmd = exec.Command("kubectl", "wait", "deployment.apps/kustomize-podinfo",
				"--for", "condition=Available",
				"--namespace", "default",
				"--timeout", "1m",
			)
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("validating that the localization was successful")
			verifyFunc := utils.GetVerifyPodFieldFunc("app=kustomize-podinfo", "jsonpath=\"{.items[0].spec.containers[?(@.name=='app')].image}\"", internalImageReference)
			EventuallyWithOffset(1, verifyFunc, time.Minute, time.Second).Should(Succeed())
		})
	})
})

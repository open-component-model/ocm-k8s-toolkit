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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/open-component-model/ocm-k8s-toolkit/test/utils"
)

var _ = Describe("controller", func() {
	Context("Operator", func() {
		It("should deploy a helm resource", func() {
			By("checking for the helm controller")
			// Note: Namespace is taken from helm-controller default kustomization
			cmd := exec.Command("kubectl", "wait", "deployment.apps/helm-controller",
				"--for", "condition=Available",
				"--namespace", "helm-system",
				"--timeout", timeout,
			)
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			testdata := os.Getenv("TESTDATA_HELM")
			if testdata == "" {
				testdata = filepath.Join(os.Getenv("PROJECT_DIR"), "test/e2e/testdata/helm-release")
			}

			// TODO: Adjust/Remove when https://github.com/open-component-model/ocm-k8s-toolkit/pull/72 is merged
			helmChart := os.Getenv("HELM_CHART")
			Expect(helmChart).NotTo(BeEmpty())

			Expect(utils.PrepareOCMComponent(
				filepath.Join(testdata, "component-constructor.yaml"),
				imageRegistry,
				"HelmChart="+helmChart,
				"LocalizationConfigPath=localization-config.yaml",
				// Note: Trim 'http://' in case of insecure registry
				"ImageReference="+strings.TrimLeft(imageReference, "http://"),
			)).To(Succeed())

			Expect(utils.DeployOCMComponents(filepath.Join(testdata, "manifests"), internalImageRegistry, timeout)).To(Succeed())

			By("validating that the resource was deployed successfully through the helm-controller")
			cmd = exec.Command("kubectl", "wait", "deployment.apps/helm-flux-podinfo",
				"--for", "condition=Available",
				"--namespace", "default",
				"--timeout", timeout,
			)
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("validating that the localization was successful")
			// Note: Trim 'http://' in case of insecure registry
			verifyFunc := utils.GetVerifyPodFieldFunc("app.kubernetes.io/name=helm-flux-podinfo", "jsonpath=\"{.items[0].spec.containers[?(@.name=='podinfo')].image}\"", strings.TrimLeft(imageReference, "http://"))
			EventuallyWithOffset(1, verifyFunc, time.Minute, time.Second).Should(Succeed())
		})

		It("should deploy a kustomize resource", func() {
			By("checking for the kustomize controller")
			// Note: Namespace is taken from helm-controller default kustomization
			cmd := exec.Command("kubectl", "wait", "deployment.apps/kustomize-controller",
				"--for", "condition=Available",
				"--namespace", "kustomize-system",
				"--timeout", timeout,
			)
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			testdata := os.Getenv("TESTDATA_KUSTOMIZE")
			if testdata == "" {
				testdata = filepath.Join(os.Getenv("PROJECT_DIR"), "test/e2e/testdata/kustomize-release")
			}

			Expect(utils.PrepareOCMComponent(
				filepath.Join(testdata, "component-constructor.yaml"),
				imageRegistry,
				"KustomizationPath=kustomization",
				"LocalizationConfigPath=localization-config.yaml",
				// Note: Trim 'http://' in case of insecure registry
				"ImageReference="+strings.TrimLeft(imageReference, "http://"),
			)).To(Succeed())

			Expect(utils.DeployOCMComponents(filepath.Join(testdata, "manifests"), internalImageRegistry, timeout)).To(Succeed())

			By("validating that the resource was deployed successfully through the kustomize-controller")
			cmd = exec.Command("kubectl", "wait", "deployment.apps/kustomize-podinfo",
				"--for", "condition=Available",
				"--namespace", "default",
				"--timeout", timeout,
			)
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("validating that the localization was successful")
			// Note: Trim 'http://' in case of insecure registry
			verifyFunc := utils.GetVerifyPodFieldFunc("app=kustomize-podinfo", "jsonpath=\"{.items[0].spec.containers[?(@.name=='app')].image}\"", strings.TrimLeft(imageReference, "http://"))
			EventuallyWithOffset(1, verifyFunc, time.Minute, time.Second).Should(Succeed())
		})
	})
})

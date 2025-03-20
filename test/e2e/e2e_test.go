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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/open-component-model/ocm-k8s-toolkit/test/utils"
)

var _ = Describe("controller", func() {
	Context("Operator", func() {
		It("should deploy a helm resource", func() {
			// TODO: https://github.com/open-component-model/ocm-k8s-toolkit/issues/154
			testdata := os.Getenv("TESTDATA_HELM")
			if testdata == "" {
				testdata = filepath.Join(os.Getenv("PROJECT_DIR"), "test/e2e/testdata/helm-release")
			}

			Expect(utils.PrepareOCMComponent(
				filepath.Join(testdata, "component-constructor.yaml"),
				imageRegistry,
			)).To(Succeed())

			By("creating and validating the custom resource OCM repository")
			Expect(utils.DeployAndWaitForResource(filepath.Join(testdata, "ocm-base.yaml"), "condition=Ready", timeout))

			By("creating and validating the custom resource for the release")
			Expect(utils.DeployAndWaitForResource(filepath.Join(testdata, "flux-deployment.yaml"), "condition=Ready", timeout))

			By("validating that the resource was deployed successfully through the helm-controller")
			cmd := exec.Command("kubectl", "wait", "deployment.apps/helm-podinfo",
				"--for", "condition=Available",
				"--namespace", "default",
				"--timeout", timeout,
			)
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
		})

		It("should deploy a kustomize resource", func() {
			// TODO: https://github.com/open-component-model/ocm-k8s-toolkit/issues/154
			testdata := os.Getenv("TESTDATA_KUSTOMIZE")
			if testdata == "" {
				testdata = filepath.Join(os.Getenv("PROJECT_DIR"), "test/e2e/testdata/kustomize-release")
			}

			Expect(utils.PrepareOCMComponent(
				filepath.Join(testdata, "component-constructor.yaml"),
				imageRegistry,
			)).To(Succeed())

			By("creating and validating the custom resource OCM repository")
			Expect(utils.DeployAndWaitForResource(filepath.Join(testdata, "ocm-base.yaml"), "condition=Ready", timeout))

			By("creating and validating the custom resource for the release")
			Expect(utils.DeployAndWaitForResource(filepath.Join(testdata, "flux-deployment.yaml"), "condition=Ready", timeout))

			By("validating that the resource was deployed successfully through the kustomize-controller")
			cmd := exec.Command("kubectl", "wait", "deployment.apps/kustomize-podinfo",
				"--for", "condition=Available",
				"--namespace", "default",
				"--timeout", timeout,
			)
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
		})
	})
})

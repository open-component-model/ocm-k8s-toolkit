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
	"path/filepath"
	"strconv"

	. "github.com/mandelsoft/goutils/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/open-component-model/ocm-k8s-toolkit/test/utils"
	testrepl "github.com/open-component-model/ocm-k8s-toolkit/test/utils/replication"
)

var _ = Describe("Replication Controller", func() {
	Context("when transferring component versions (OCI)", func() {
		const testNamespace = "ns-e2e-test-replication-controller"
		const compOCMName = "ocm.software/replication-controller/e2e-test"
		const compVersion = "0.1.0"

		var pathPattern string
		var iteration = 0
		var (
			sourceRepoResourceName  string
			sourceRepoManifestFile  string
			componentResourceName   string
			componentManifestFile   string
			targetRepoResourceName  string
			targetRepoManifestFile  string
			replicationResourceName string
			replicationManifestFile string
		)

		BeforeEach(func() {
			err := utils.CreateNamespace(testNamespace)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			DeferCleanup(func() error {
				return utils.DeleteNamespace(testNamespace)
			})

			iteration++
			i := strconv.Itoa(iteration)
			pathPattern = "ocm-k8s-replication-e2e-test" + i + "--*"

			sourceRepoResourceName = "test-source-repository" + i
			sourceRepoManifestFile = "source-OCMRepository" + i + ".yaml"
			componentResourceName = "test-component" + i
			componentManifestFile = "Component" + i + ".yaml"
			targetRepoResourceName = "test-target-repository" + i
			targetRepoManifestFile = "target-OCMRepository" + i + ".yaml"
			replicationResourceName = "test-replication" + i
			replicationManifestFile = "Replication" + i + ".yaml"
		})

		It("should be possible to transfer OCM CLI from official location to provided OCI registry", func() {
			By("Create temporary directory")
			tmpDir := Must(os.MkdirTemp("", pathPattern))
			DeferCleanup(func() error {
				return os.RemoveAll(tmpDir)
			})

			By("Create k8s resources")
			sourceRepo, _ := testrepl.NewTestOCIRepository(testNamespace, sourceRepoResourceName, testrepl.TestExternalRegistry)
			ocmCompName := testrepl.TestExternalComponent
			ocmCompVersion := testrepl.TestExternalVersion
			comp := testrepl.NewTestComponent(testNamespace, componentResourceName, sourceRepo.Name, ocmCompName, ocmCompVersion)
			// Use internal registry URL, because this is an in-cluster operation
			targetRepoURL := internalImageRegistry
			targetRepo, _ := testrepl.NewTestOCIRepository(testNamespace, targetRepoResourceName, targetRepoURL)
			replication := testrepl.NewTestReplication(testNamespace, replicationResourceName, comp.Name, targetRepo.Name)

			By("Serialize k8s resources to manifest files")
			sourceRepoManifestFile = filepath.Join(tmpDir, sourceRepoManifestFile)
			Expect(testrepl.SaveToManifest(sourceRepo, sourceRepoManifestFile)).To(Succeed())

			componentManifestFile = filepath.Join(tmpDir, componentManifestFile)
			Expect(testrepl.SaveToManifest(comp, componentManifestFile)).To(Succeed())

			targetRepoManifestFile = filepath.Join(tmpDir, targetRepoManifestFile)
			Expect(testrepl.SaveToManifest(targetRepo, targetRepoManifestFile)).To(Succeed())

			replicationManifestFile = filepath.Join(tmpDir, replicationManifestFile)
			Expect(testrepl.SaveToManifest(replication, replicationManifestFile)).To(Succeed())

			By("Apply manifests to the cluster")
			Expect(utils.DeployAndWaitForResource(sourceRepoManifestFile, "condition=Ready", timeout)).To(Succeed())
			Expect(utils.DeployAndWaitForResource(componentManifestFile, "condition=Ready", timeout)).To(Succeed())
			Expect(utils.DeployAndWaitForResource(targetRepoManifestFile, "condition=Ready", timeout)).To(Succeed())
			Expect(utils.DeployAndWaitForResource(replicationManifestFile, "condition=Ready", timeout)).To(Succeed())

			By("Double-check that copied component version is present in the target repository")
			// Use external registry URL, because the check connects from outside
			targetRepoURL = imageRegistry
			Expect(utils.CheckOCMComponent(targetRepoURL+"//"+ocmCompName+":"+ocmCompVersion, "")).To(Succeed())
		})
	})
})

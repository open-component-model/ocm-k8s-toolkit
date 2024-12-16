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
	"strings"

	. "github.com/mandelsoft/goutils/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/test/utils"
	helper "github.com/open-component-model/ocm-k8s-toolkit/test/utils/replication"
)

const OCMConfigCredentials1 = `
type: generic.config.ocm.software/v1
configurations:
  - type: credentials.config.ocm.software
    consumers:
      - identity:
          type: OCIRegistry
          hostname: localhost
          port: 31001
        credentials:
          - type: Credentials
            properties:
              username: admin
              password: admin
      - identity:
          type: OCIRegistry
          hostname: protected-registry1-internal.default.svc.cluster.local
          port: 5001
        credentials:
          - type: Credentials
            properties:
              username: admin
              password: admin
`

const OCMConfigCredentials2 = `
type: generic.config.ocm.software/v1
configurations:
  - type: credentials.config.ocm.software
    consumers:
      - identity:
          type: OCIRegistry
          hostname: localhost
          port: 31002
        credentials:
          - type: Credentials
            properties:
              username: admin2
              password: admin2
      - identity:
          type: OCIRegistry
          hostname: protected-registry2-internal.default.svc.cluster.local
          port: 5002
        credentials:
          - type: Credentials
            properties:
              username: admin2
              password: admin2
`

var _ = Describe("Replication Controller", func() {
	Context("when transferring component versions (OCI)", func() {
		// Using an existing component for the test, either podinfo or OCM CLI itself.
		// podinfo is preferred, because it has an image, which can either be copied or not,
		// depending on provided transfer options.
		const (
			externalRegistry       = "ghcr.io/open-component-model" // "ghcr.io/open-component-model/ocm"
			ocmCompName            = "ocm.software/podinfo"         // "ocm.software/ocmcli"
			ocmCompVersion         = "6.6.2"                        // "0.17.0"
			podinfoImage           = "stefanprodan/podinfo:6.6.2"
			podinfoImgResourceName = "image"
			ocmCheckOptFailOnError = "--fail-on-error"
		)

		const testNamespace = "ns-e2e-test-replication-controller"

		const (
			envProtectedRegistryURL          = "PROTECTED_REGISTRY_URL"
			envInternalProtectedRegistryURL  = "INTERNAL_PROTECTED_REGISTRY_URL"
			envProtectedRegistryURL2         = "PROTECTED_REGISTRY_URL2"
			envInternalProtectedRegistryURL2 = "INTERNAL_PROTECTED_REGISTRY_URL2"
		)

		var pathPattern string
		var iteration = 0
		var (
			sourceRepoResourceName      string
			sourceRepoManifestFile      string
			componentResourceName       string
			componentManifestFile       string
			targetRepoResourceName      string
			targetRepoManifestFile      string
			replicationResourceName     string
			replicationManifestFile     string
			transferOptionsResourceName string
			credsOptionsResourceName    string
			transferOptionsManifestFile string
			credsOptionsManifestFile    string
			ocmconfigFile1              string

			credsOptionsResourceName2 string
			credsOptionsManifestFile2 string
			ocmconfigFile2            string
			componentResourceName2    string
			componentManifestFile2    string
			targetRepoResourceName2   string
			targetRepoManifestFile2   string
			replicationResourceName2  string
			replicationManifestFile2  string
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
			transferOptionsResourceName = "test-transfer-options" + i
			transferOptionsManifestFile = "ConfigMapTransferOpt" + i + ".yaml"
			credsOptionsResourceName = "test-creds-options" + i
			credsOptionsManifestFile = "ConfigMapCreds1-" + i + ".yaml"
			ocmconfigFile1 = "creds1-" + i + ".ocmconfig"

			credsOptionsResourceName2 = "test-creds-options2-" + i
			credsOptionsManifestFile2 = "ConfigMapCreds2-" + i + ".yaml"
			ocmconfigFile2 = "creds2-" + i + ".ocmconfig"
			componentResourceName2 = "test-component2-" + i
			componentManifestFile2 = "Component2-" + i + ".yaml"
			targetRepoResourceName2 = "test-target-repository2-" + i
			targetRepoManifestFile2 = "target-OCMRepository2-" + i + ".yaml"
			replicationResourceName2 = "test-replication2-" + i
			replicationManifestFile2 = "Replication2-" + i + ".yaml"
		})

		// This test transfers the test component from a public registry to the one configured via the environment variables:
		// IMAGE_REGISTRY_URL and INTERNAL_IMAGE_REGISTRY_URL.
		// The test does not use transfer options
		It("should be possible to transfer OCM CLI from official location to provided OCI registry", func() {
			By("Create temporary directory")
			tmpDir := Must(os.MkdirTemp("", pathPattern))
			DeferCleanup(func() error {
				return os.RemoveAll(tmpDir)
			})

			By("Create k8s resources")
			sourceRepo, _ := helper.NewTestOCIRepository(testNamespace, sourceRepoResourceName, externalRegistry)
			comp := helper.NewTestComponent(testNamespace, componentResourceName, sourceRepo.Name, ocmCompName, ocmCompVersion)
			// Use internal registry URL, because this is an in-cluster operation
			targetRepoURL := internalImageRegistry
			targetRepo, _ := helper.NewTestOCIRepository(testNamespace, targetRepoResourceName, targetRepoURL)
			replication := helper.NewTestReplication(testNamespace, replicationResourceName, comp.Name, targetRepo.Name)

			By("Serialize k8s resources to manifest files")
			sourceRepoManifestFile = filepath.Join(tmpDir, sourceRepoManifestFile)
			Expect(helper.SaveToManifest(sourceRepo, sourceRepoManifestFile)).To(Succeed())
			componentManifestFile = filepath.Join(tmpDir, componentManifestFile)
			Expect(helper.SaveToManifest(comp, componentManifestFile)).To(Succeed())
			targetRepoManifestFile = filepath.Join(tmpDir, targetRepoManifestFile)
			Expect(helper.SaveToManifest(targetRepo, targetRepoManifestFile)).To(Succeed())
			replicationManifestFile = filepath.Join(tmpDir, replicationManifestFile)
			Expect(helper.SaveToManifest(replication, replicationManifestFile)).To(Succeed())

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

		// This test does two transfer operations:
		//   1. From a public registry to the one configured via the environment variables PROTECTED_REGISTRY_URL / INTERNAL_PROTECTED_REGISTRY_URL.
		//   2. From protected registry above to a yet another protected registry (PROTECTED_REGISTRY_URL2 / INTERNAL_PROTECTED_REGISTRY_URL2).
		// The protected registries are password-protected, thus respective ocmconfig are required to access them.
		// Also transfer options are used in both transfer operations.
		It("should be possible to transfer CVs between private OCI registries with transfer options", func() {
			var (
				protectedRegistry          string
				internalProtectedRegistry  string
				protectedRegistry2         string
				internalProtectedRegistry2 string
			)

			By("Checking for protected registry URLs", func() {
				protectedRegistry = os.Getenv(envProtectedRegistryURL)
				Expect(protectedRegistry).NotTo(BeEmpty())
				internalProtectedRegistry = os.Getenv(envInternalProtectedRegistryURL)
				Expect(internalProtectedRegistry).NotTo(BeEmpty())
				protectedRegistry2 = os.Getenv(envProtectedRegistryURL2)
				Expect(protectedRegistry2).NotTo(BeEmpty())
				internalProtectedRegistry2 = os.Getenv(envInternalProtectedRegistryURL2)
				Expect(internalProtectedRegistry2).NotTo(BeEmpty())
			})

			By("Create temporary directory")
			tmpDir := Must(os.MkdirTemp("", pathPattern))
			DeferCleanup(func() error {
				return os.RemoveAll(tmpDir)
			})

			By("Create k8s resources")
			sourceRepo, _ := helper.NewTestOCIRepository(testNamespace, sourceRepoResourceName, externalRegistry)
			comp := helper.NewTestComponent(testNamespace, componentResourceName, sourceRepo.Name, ocmCompName, ocmCompVersion)
			transferOptionsConfigMap := helper.NewTestConfigMapForData(testNamespace, transferOptionsResourceName, helper.OCMConfigResourcesByValue)
			credsConfigMap := helper.NewTestConfigMapForData(testNamespace, credsOptionsResourceName, OCMConfigCredentials1)

			// Use internal registry URL, because this is an in-cluster operation
			targetRepoURL := internalProtectedRegistry
			targetRepo, _ := helper.NewTestOCIRepository(testNamespace, targetRepoResourceName, targetRepoURL)
			targetRepo.Spec.ConfigRefs = []corev1.LocalObjectReference{
				{Name: credsConfigMap.Name},
			}
			replication := helper.NewTestReplication(testNamespace, replicationResourceName, comp.Name, targetRepo.Name)
			replication.Spec.ConfigRefs = []corev1.LocalObjectReference{
				{Name: transferOptionsConfigMap.Name},
			}

			By("Serialize k8s resources to manifest files")
			sourceRepoManifestFile = filepath.Join(tmpDir, sourceRepoManifestFile)
			Expect(helper.SaveToManifest(sourceRepo, sourceRepoManifestFile)).To(Succeed())
			componentManifestFile = filepath.Join(tmpDir, componentManifestFile)
			Expect(helper.SaveToManifest(comp, componentManifestFile)).To(Succeed())
			transferOptionsManifestFile = filepath.Join(tmpDir, transferOptionsManifestFile)
			Expect(helper.SaveToManifest(transferOptionsConfigMap, transferOptionsManifestFile)).To(Succeed())
			credsOptionsManifestFile = filepath.Join(tmpDir, credsOptionsManifestFile)
			Expect(helper.SaveToManifest(credsConfigMap, credsOptionsManifestFile)).To(Succeed())
			targetRepoManifestFile = filepath.Join(tmpDir, targetRepoManifestFile)
			Expect(helper.SaveToManifest(targetRepo, targetRepoManifestFile)).To(Succeed())
			replicationManifestFile = filepath.Join(tmpDir, replicationManifestFile)
			Expect(helper.SaveToManifest(replication, replicationManifestFile)).To(Succeed())

			By("Apply manifests to the cluster")
			Expect(utils.DeployResource(transferOptionsManifestFile)).To(Succeed())
			Expect(utils.DeployResource(credsOptionsManifestFile)).To(Succeed())
			Expect(utils.DeployAndWaitForResource(sourceRepoManifestFile, "condition=Ready", timeout)).To(Succeed())
			Expect(utils.DeployAndWaitForResource(componentManifestFile, "condition=Ready", timeout)).To(Succeed())
			Expect(utils.DeployAndWaitForResource(targetRepoManifestFile, "condition=Ready", timeout)).To(Succeed())
			Expect(utils.DeployAndWaitForResource(replicationManifestFile, "condition=Ready", timeout)).To(Succeed())

			By("Double-check that copied component version is present in the destination private registry")
			// Credentials are required for the 'ocm check' command to access the protected registry.
			ocmconfigFile1 = filepath.Join(tmpDir, ocmconfigFile1)
			Expect(helper.SaveToFile(ocmconfigFile1, []byte(OCMConfigCredentials1))).To(Succeed())
			// Use external registry URL, because the check connects from outside.
			targetRepoURL = protectedRegistry
			Expect(utils.CheckOCMComponent(targetRepoURL+"//"+ocmCompName+":"+ocmCompVersion, ocmconfigFile1, ocmCheckOptFailOnError)).To(Succeed())

			By("Set up resources to transfer the CV further (from one protected registry to another protected registry)")
			// Previous target is now the new source. Btw., the resource already exists in the cluster.
			sourceRepo = targetRepo
			// New component resource to watch the protected registry.
			comp = helper.NewTestComponent(testNamespace, componentResourceName2, sourceRepo.Name, ocmCompName, ocmCompVersion)
			// Credentials for the second protected registry.
			credsConfigMap2 := helper.NewTestConfigMapForData(testNamespace, credsOptionsResourceName2, OCMConfigCredentials2)

			// New target repo is the second protected registry.
			targetRepoURL = internalProtectedRegistry2
			targetRepo, _ = helper.NewTestOCIRepository(testNamespace, targetRepoResourceName2, targetRepoURL)
			targetRepo.Spec.ConfigRefs = []corev1.LocalObjectReference{
				{Name: credsConfigMap2.Name},
			}
			replication = helper.NewTestReplication(testNamespace, replicationResourceName2, comp.Name, targetRepo.Name)
			replication.Spec.ConfigRefs = []corev1.LocalObjectReference{
				{Name: transferOptionsConfigMap.Name}, // Re-use existing ConfigMap with transfer options.
			}

			By("Serialize k8s resources to manifest files")
			componentManifestFile2 = filepath.Join(tmpDir, componentManifestFile2)
			Expect(helper.SaveToManifest(comp, componentManifestFile2)).To(Succeed())
			credsOptionsManifestFile2 = filepath.Join(tmpDir, credsOptionsManifestFile2)
			Expect(helper.SaveToManifest(credsConfigMap2, credsOptionsManifestFile2)).To(Succeed())
			targetRepoManifestFile2 = filepath.Join(tmpDir, targetRepoManifestFile2)
			Expect(helper.SaveToManifest(targetRepo, targetRepoManifestFile2)).To(Succeed())
			replicationManifestFile2 = filepath.Join(tmpDir, replicationManifestFile2)
			Expect(helper.SaveToManifest(replication, replicationManifestFile2)).To(Succeed())

			By("Apply manifests to the cluster")
			Expect(utils.DeployResource(credsOptionsManifestFile2)).To(Succeed())
			Expect(utils.DeployAndWaitForResource(componentManifestFile2, "condition=Ready", timeout)).To(Succeed())
			Expect(utils.DeployAndWaitForResource(targetRepoManifestFile2, "condition=Ready", timeout)).To(Succeed())
			Expect(utils.DeployAndWaitForResource(replicationManifestFile2, "condition=Ready", timeout)).To(Succeed())

			By("Double-check that copied component version is present in the second private registry")
			// Credentials are required for the 'ocm check' command to access the protected registry.
			ocmconfigFile2 = filepath.Join(tmpDir, ocmconfigFile2)
			Expect(helper.SaveToFile(ocmconfigFile2, []byte(OCMConfigCredentials2))).To(Succeed())
			// Use external registry URL, because the check connects from outside.
			targetRepoURL = protectedRegistry2
			componentReference := targetRepoURL + "//" + ocmCompName + ":" + ocmCompVersion
			Expect(utils.CheckOCMComponent(componentReference, ocmconfigFile2, ocmCheckOptFailOnError)).To(Succeed())

			By("Double-check that \"resourcesByValue\" transfer option has been applied")
			// I.e. that the resource's imageReference points to the correct registry .
			// Example reference:
			// "http://protected-registry2-internal.default.svc.cluster.local:5002/stefanprodan/podinfo:6.6.2@sha256:4aa3b819f4cafc97d03d902ed17cbec076e2beee02d53b67ff88527124086fd9"
			imgRef, err := utils.GetOCMResourceImageRef(componentReference, podinfoImgResourceName, ocmconfigFile2)
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.HasPrefix(imgRef, internalProtectedRegistry2+"/"+podinfoImage)).Should(BeTrue())
		})
	})
})

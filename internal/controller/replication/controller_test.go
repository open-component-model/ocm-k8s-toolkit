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

package replication

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	mandelsoft "github.com/mandelsoft/goutils/testutils"
	"github.com/mandelsoft/vfs/pkg/osfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ocmbuilder "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"
	ocmmetav1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
	resourcetypes "ocm.software/ocm/api/ocm/extensions/artifacttypes"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/ocm/extensions/repositories/genericocireg"
	"ocm.software/ocm/api/utils/accessio"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

const reconciliationInterval = time.Second * 3

const OCMConfigResourcesByValue = `
type: generic.config.ocm.software/v1
configurations:
  - type: transport.ocm.config.ocm.software
    recursive: true
    overwrite: true
    localResourcesByValue: false
    resourcesByValue: true
    sourcesByValue: false
    keepGlobalAccess: false
    stopOnExistingVersion: false
`

var _ = Describe("Replication Controller", func() {
	Context("when transferring component versions (CTFs)", func() {
		const (
			testNamespace = "replication-controller-test"
			compOCMName   = "ocm.software/component-for-replication"
			compVersion   = "0.1.0"

			// "gcr.io/google_containers/echoserver:1.10" is taken as test content, because it is used to explain OCM here:
			// https://ocm.software/docs/getting-started/getting-started-with-ocm/create-a-component-version/#add-an-image-reference-option-1
			testImage      = "gcr.io/google_containers/echoserver:1.10"
			localImagePath = "blobs/sha256.4b93359cc643b5d8575d4f96c2d107b4512675dcfee1fa035d0c44a00b9c027c"
		)

		var (
			ctx       context.Context
			cancel    context.CancelFunc
			namespace *corev1.Namespace
			env       *ocmbuilder.Builder
		)

		var (
			replResourceName       string
			compResourceName       string
			optResourceName        string
			sourceRepoResourceName string
			targetRepoResourceName string
			sourcePattern          string
			targetPattern          string
		)

		var replNamespacedName types.NamespacedName

		var iteration = 0
		var maxTimeToReconcile = 5 * time.Minute

		BeforeEach(func() {
			env = ocmbuilder.NewBuilder(environment.FileSystem(osfs.OsFs))
			DeferCleanup(env.Cleanup)

			ctx, cancel = context.WithCancel(context.Background())
			DeferCleanup(cancel)

			if namespace == nil {
				namespace = &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNamespace,
					},
				}
				Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
			}

			iteration++
			i := strconv.Itoa(iteration)
			replResourceName = "test-replication" + i
			compResourceName = "test-component" + i
			optResourceName = "test-transfer-options" + i
			sourceRepoResourceName = "test-source-repository" + i
			targetRepoResourceName = "test-target-repository" + i
			sourcePattern = "ocm-k8s-replication-source" + i + "--*"
			targetPattern = "ocm-k8s-replication-target" + i + "--*"

			replNamespacedName = types.NamespacedName{
				Name:      replResourceName,
				Namespace: testNamespace,
			}
		})

		AfterEach(func() {
		})

		It("should be properly reflected in the history", func() {
			By("Create source CTF")
			sourcePath := mandelsoft.Must(os.MkdirTemp("", sourcePattern))
			DeferCleanup(func() error {
				return os.RemoveAll(sourcePath)
			})

			newTestComponentVersionInCTFDir(env, sourcePath, compOCMName, compVersion, testImage)

			By("Create source repository resource")
			sourceRepo, sourceSpecData := newTestCFTRepository(testNamespace, sourceRepoResourceName, sourcePath)
			Expect(k8sClient.Create(ctx, sourceRepo)).To(Succeed())

			By("Simulate ocmrepository controller for source repository")
			conditions.MarkTrue(sourceRepo, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, sourceRepo)).To(Succeed())

			By("Create source component resource")
			component := newTestComponent(testNamespace, compResourceName, sourceRepoResourceName, compOCMName, compVersion)
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("Simulate component controller")
			component.Status.Component = *newTestComponentInfo(compOCMName, compVersion, sourceSpecData)
			conditions.MarkTrue(component, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, component)).To(Succeed())

			By("Create target CTF")
			targetPath := mandelsoft.Must(os.MkdirTemp("", targetPattern))
			DeferCleanup(func() error {
				return os.RemoveAll(targetPath)
			})

			By("Create target repository resource")
			targetRepo, targetSpecData := newTestCFTRepository(testNamespace, targetRepoResourceName, targetPath)
			Expect(k8sClient.Create(ctx, targetRepo)).To(Succeed())

			By("Simulate ocmrepository controller for target repository")
			targetRepo.Spec.RepositorySpec.Raw = *targetSpecData
			conditions.MarkTrue(targetRepo, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, targetRepo)).To(Succeed())

			By("Create and reconcile Replication resource")
			replication := newTestReplication(testNamespace, replResourceName, compResourceName, targetRepoResourceName)
			Expect(k8sClient.Create(ctx, replication)).To(Succeed())

			replication = &v1alpha1.Replication{}
			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, replNamespacedName, replication)).To(Succeed())
				// TODO: with IsReady only the test flickers. Why is it not sufficient???
				return conditions.IsReady(replication) && replication.Status.ObservedGeneration > 0
			}).WithTimeout(maxTimeToReconcile).Should(BeTrue())

			Expect(replication.Status.History).To(HaveLen(1))
			Expect(replication.Status.History[0].Component).To(Equal(compOCMName))
			Expect(replication.Status.History[0].Version).To(Equal(compVersion))
			Expect(replication.Status.History[0].SourceRepositorySpec).To(Equal(string(*sourceSpecData)))
			Expect(replication.Status.History[0].TargetRepositorySpec).To(Equal(string(*targetSpecData)))
			Expect(replication.Status.History[0].StartTime).NotTo(BeZero())
			Expect(replication.Status.History[0].EndTime).NotTo(BeZero())
			Expect(replication.Status.History[0].Error).To(BeEmpty())
			Expect(replication.Status.History[0].Success).To(BeTrue())

			By("Create a newer component version")
			compNewVersion := "0.2.0"
			newTestComponentVersionInCTFDir(env, sourcePath, compOCMName, compNewVersion, testImage)

			By("Simulate component controller discovering the newer version")
			component.Status.Component = *newTestComponentInfo(compOCMName, compNewVersion, sourceSpecData)
			conditions.MarkTrue(component, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, component)).To(Succeed())

			By("Expect Replication controller to transfer the new version within the interval")
			waitingTime := replication.GetRequeueAfter() + maxTimeToReconcile
			replication = &v1alpha1.Replication{}
			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, replNamespacedName, replication)).To(Succeed())
				// Wait for the second entry in the history
				return conditions.IsReady(replication) && len(replication.Status.History) == 2
			}).WithTimeout(waitingTime).Should(BeTrue())

			// Expect see the new component version in the history
			Expect(replication.Status.History[1].Version).To(Equal(compNewVersion))

			By("Cleanup the resources")
			Expect(k8sClient.Delete(ctx, replication)).To(Succeed())
			Expect(k8sClient.Delete(ctx, targetRepo)).To(Succeed())
			Expect(k8sClient.Delete(ctx, component)).To(Succeed())
			Expect(k8sClient.Delete(ctx, sourceRepo)).To(Succeed())
		})

		It("should be possible to configure transfer options", func() {

			By("Create source CTF")
			sourcePath := mandelsoft.Must(os.MkdirTemp("", sourcePattern))
			DeferCleanup(func() error {
				return os.RemoveAll(sourcePath)
			})

			newTestComponentVersionInCTFDir(env, sourcePath, compOCMName, compVersion, testImage)

			By("Create source repository resource")
			sourceRepo, sourceSpecData := newTestCFTRepository(testNamespace, sourceRepoResourceName, sourcePath)
			Expect(k8sClient.Create(ctx, sourceRepo)).To(Succeed())

			By("Simulate ocmrepository controller for source repository")
			conditions.MarkTrue(sourceRepo, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, sourceRepo)).To(Succeed())

			By("Create source component resource")
			component := newTestComponent(testNamespace, compResourceName, sourceRepoResourceName, compOCMName, compVersion)
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("Simulate component controller")
			component.Status.Component = *newTestComponentInfo(compOCMName, compVersion, sourceSpecData)
			conditions.MarkTrue(component, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, component)).To(Succeed())

			By("Create target CTF")
			targetPath := mandelsoft.Must(os.MkdirTemp("", targetPattern))
			DeferCleanup(func() error {
				return os.RemoveAll(targetPath)
			})

			By("Create target repository resource")
			targetRepo, targetSpecData := newTestCFTRepository(testNamespace, targetRepoResourceName, targetPath)
			Expect(k8sClient.Create(ctx, targetRepo)).To(Succeed())

			By("Simulate ocmrepository controller for target repository")
			targetRepo.Spec.RepositorySpec.Raw = *targetSpecData
			conditions.MarkTrue(targetRepo, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, targetRepo)).To(Succeed())

			By("Create ConfigMap with transfer options")
			configMap := newTestConfigMapForData(testNamespace, optResourceName, OCMConfigResourcesByValue)
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			By("Create and reconcile Replication resource")
			replication := newTestReplication(testNamespace, replResourceName, compResourceName, targetRepoResourceName)
			replication.Spec.ConfigRefs = []corev1.LocalObjectReference{
				{Name: optResourceName},
			}
			Expect(k8sClient.Create(ctx, replication)).To(Succeed())

			By("Wait for reconciliation to run")
			replication = &v1alpha1.Replication{}
			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, replNamespacedName, replication)).To(Succeed())
				return conditions.IsReady(replication) && len(replication.Status.History) == 1
			}).WithTimeout(maxTimeToReconcile).Should(BeTrue())

			// Expect to see the transfered component version in the history
			Expect(replication.Status.History[0].Version).To(Equal(compVersion))

			// Check the effect of transfer options.
			// This docker image was downloaded due to 'resourcesByValue: true' set in the transfer options
			imageArtifact := filepath.Join(targetPath, localImagePath)
			Expect(imageArtifact).To(BeAnExistingFile())

			By("Cleanup the resources")
			Expect(k8sClient.Delete(ctx, configMap)).To(Succeed())
			Expect(k8sClient.Delete(ctx, replication)).To(Succeed())
			Expect(k8sClient.Delete(ctx, targetRepo)).To(Succeed())
			Expect(k8sClient.Delete(ctx, component)).To(Succeed())
			Expect(k8sClient.Delete(ctx, sourceRepo)).To(Succeed())
		})

		It("transfer errors should be properly reflected in the history", func() {
			By("Create source CTF")
			sourcePath := mandelsoft.Must(os.MkdirTemp("", sourcePattern))
			DeferCleanup(func() error {
				return os.RemoveAll(sourcePath)
			})

			// The created directory is empty, i.e. the test will try to transfer a non-existing component version.
			// This should result in an error, logged in the status of the replication object.

			By("Create source repository resource")
			sourceRepo, sourceSpecData := newTestCFTRepository(testNamespace, sourceRepoResourceName, sourcePath)
			Expect(k8sClient.Create(ctx, sourceRepo)).To(Succeed())

			By("Simulate ocmrepository controller for source repository")
			conditions.MarkTrue(sourceRepo, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, sourceRepo)).To(Succeed())

			By("Create source component resource")
			component := newTestComponent(testNamespace, compResourceName, sourceRepoResourceName, compOCMName, compVersion)
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("Simulate component controller")
			component.Status.Component = *newTestComponentInfo(compOCMName, compVersion, sourceSpecData)
			conditions.MarkTrue(component, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, component)).To(Succeed())

			By("Create target CTF")
			targetPath := mandelsoft.Must(os.MkdirTemp("", targetPattern))
			DeferCleanup(func() error {
				return os.RemoveAll(targetPath)
			})

			By("Create target repository resource")
			targetRepo, targetSpecData := newTestCFTRepository(testNamespace, targetRepoResourceName, targetPath)
			Expect(k8sClient.Create(ctx, targetRepo)).To(Succeed())

			By("Simulate ocmrepository controller for target repository")
			targetRepo.Spec.RepositorySpec.Raw = *targetSpecData
			conditions.MarkTrue(targetRepo, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, targetRepo)).To(Succeed())

			By("Create and reconcile Replication resource")
			replication := newTestReplication(testNamespace, replResourceName, compResourceName, targetRepoResourceName)
			Expect(k8sClient.Create(ctx, replication)).To(Succeed())

			// Check that the reconciliation consistently fails (due to physically non-existing component version).
			// The assumption here is that after the first error k8s will apply a backoff strategy that will trigger
			// a couple of more reconciliation attempts during the waiting time.
			waitingTime := 3 * time.Second // interval during which to collect failed reconciliation attempts
			errorCounter := 0
			var errorTimestamp metav1.Time
			Consistently(func() bool {
				replication = &v1alpha1.Replication{}
				Expect(k8sClient.Get(ctx, replNamespacedName, replication)).To(Succeed())

				isReady := conditions.IsReady(replication)
				if isReady { // false is expected
					return isReady
				}

				historyLen := len(replication.Status.History)
				if historyLen > 0 && replication.Status.History[historyLen-1].EndTime.After(errorTimestamp.Time) {
					errorCounter++
					errorTimestamp = replication.Status.History[historyLen-1].EndTime
				}

				return isReady
			}, waitingTime).Should(BeFalse())

			// Check that there have been multiple reconciliation attempts.
			Expect(errorCounter).To(BeNumerically(">", 1))

			// Only one history entry with the error is expected, despite multiple reconciliation attempts.
			Expect(len(replication.Status.History)).To(Equal(1))
			Expect(conditions.IsReady(replication)).To(BeFalse())
			Expect(replication.Status.History[0].Success).To(BeFalse())
			Expect(replication.Status.History[0].Error).To(HavePrefix("cannot lookup component version in source repository: component version \"" + compOCMName + ":" + compVersion + "\" not found"))

			// Check that the other fields are properly set.
			Expect(replication.Status.History[0].Component).To(Equal(compOCMName))
			Expect(replication.Status.History[0].Version).To(Equal(compVersion))
			Expect(replication.Status.History[0].SourceRepositorySpec).To(Equal(string(*sourceSpecData)))
			Expect(replication.Status.History[0].TargetRepositorySpec).To(Equal(string(*targetSpecData)))
			Expect(replication.Status.History[0].StartTime).NotTo(BeZero())
			Expect(replication.Status.History[0].EndTime).NotTo(BeZero())

			By("Cleanup the resources")
			Expect(k8sClient.Delete(ctx, replication)).To(Succeed())
			Expect(k8sClient.Delete(ctx, targetRepo)).To(Succeed())
			Expect(k8sClient.Delete(ctx, component)).To(Succeed())
			Expect(k8sClient.Delete(ctx, sourceRepo)).To(Succeed())
		})
	})
})

func newTestComponentVersionInCTFDir(env *ocmbuilder.Builder, path, compName, compVersion, img string) {
	env.OCMCommonTransport(path, accessio.FormatDirectory, func() {
		env.Component(compName, func() {
			env.Version(compVersion, func() {
				env.Resource("image", "1.0.0", resourcetypes.OCI_IMAGE, ocmmetav1.ExternalRelation, func() {
					env.Access(
						ociartifact.New(img),
					)
				})
			})
		})
	})
}

func newTestCFTRepository(namespace, name, path string) (*v1alpha1.OCMRepository, *[]byte) {
	spec := mandelsoft.Must(ctf.NewRepositorySpec(ctf.ACC_CREATE, path))

	return newTestOCMRepository(namespace, name, spec)
}

func newTestOCMRepository(namespace, name string, spec *genericocireg.RepositorySpec) (*v1alpha1.OCMRepository, *[]byte) {
	specData := mandelsoft.Must(spec.MarshalJSON())

	return &v1alpha1.OCMRepository{
		TypeMeta: metav1.TypeMeta{
			Kind:       "OCMRepository",
			APIVersion: v1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: v1alpha1.OCMRepositorySpec{
			RepositorySpec: &apiextensionsv1.JSON{
				Raw: specData,
			},
			Interval: metav1.Duration{Duration: reconciliationInterval},
		},
	}, &specData
}

func newTestComponent(namespace, name, repoName, ocmName, ocmVersion string) *v1alpha1.Component {
	return &v1alpha1.Component{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Component",
			APIVersion: v1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: v1alpha1.ComponentSpec{
			RepositoryRef: v1alpha1.ObjectKey{
				Namespace: namespace,
				Name:      repoName,
			},
			Component: ocmName,
			Semver:    ocmVersion,
			Interval:  metav1.Duration{Duration: reconciliationInterval},
		},
	}
}

func newTestComponentInfo(ocmName, ocmVersion string, rawRepoSpec *[]byte) *v1alpha1.ComponentInfo {
	return &v1alpha1.ComponentInfo{
		RepositorySpec: &apiextensionsv1.JSON{Raw: *rawRepoSpec},
		Component:      ocmName,
		Version:        ocmVersion,
	}
}

func newTestReplication(namespace, name, compName, targetRepoName string) *v1alpha1.Replication {
	return &v1alpha1.Replication{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Replication",
			APIVersion: v1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ReplicationSpec{
			ComponentRef: v1alpha1.ObjectKey{
				Name:      compName,
				Namespace: namespace,
			},
			TargetRepositoryRef: v1alpha1.ObjectKey{
				Name:      targetRepoName,
				Namespace: namespace,
			},
			Interval: metav1.Duration{Duration: reconciliationInterval},
		},
	}
}

func newTestConfigMapForData(namespace, name, data string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Data: map[string]string{
			v1alpha1.OCMConfigKey: data,
		},
	}
}

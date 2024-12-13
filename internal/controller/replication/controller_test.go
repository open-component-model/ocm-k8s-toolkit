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
	. "github.com/mandelsoft/goutils/testutils"
	"github.com/mandelsoft/vfs/pkg/osfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	. "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	helper "github.com/open-component-model/ocm-k8s-toolkit/test/utils/replication"
)

var _ = Describe("Replication Controller", func() {
	Context("when transferring component versions (CTFs)", func() {
		const (
			testNamespace = "ns-test-replication-controller"
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
			env       *Builder
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
			env = NewBuilder(environment.FileSystem(osfs.OsFs))
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
			sourcePath := Must(os.MkdirTemp("", sourcePattern))
			DeferCleanup(func() error {
				return os.RemoveAll(sourcePath)
			})

			helper.NewTestComponentVersionInCTFDir(env, sourcePath, compOCMName, compVersion, testImage)

			By("Create source repository resource")
			sourceRepo, sourceSpecData := helper.NewTestCFTRepository(testNamespace, sourceRepoResourceName, sourcePath)
			Expect(k8sClient.Create(ctx, sourceRepo)).To(Succeed())

			By("Simulate ocmrepository controller for source repository")
			conditions.MarkTrue(sourceRepo, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, sourceRepo)).To(Succeed())

			By("Create source component resource")
			component := helper.NewTestComponent(testNamespace, compResourceName, sourceRepoResourceName, compOCMName, compVersion)
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("Simulate component controller")
			component.Status.Component = *helper.NewTestComponentInfo(compOCMName, compVersion, sourceSpecData)
			conditions.MarkTrue(component, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, component)).To(Succeed())

			By("Create target CTF")
			targetPath := Must(os.MkdirTemp("", targetPattern))
			DeferCleanup(func() error {
				return os.RemoveAll(targetPath)
			})

			By("Create target repository resource")
			targetRepo, targetSpecData := helper.NewTestCFTRepository(testNamespace, targetRepoResourceName, targetPath)
			Expect(k8sClient.Create(ctx, targetRepo)).To(Succeed())

			By("Simulate ocmrepository controller for target repository")
			targetRepo.Spec.RepositorySpec.Raw = *targetSpecData
			conditions.MarkTrue(targetRepo, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, targetRepo)).To(Succeed())

			By("Create and reconcile Replication resource")
			replication := helper.NewTestReplication(testNamespace, replResourceName, compResourceName, targetRepoResourceName)
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
			helper.NewTestComponentVersionInCTFDir(env, sourcePath, compOCMName, compNewVersion, testImage)

			By("Simulate component controller discovering the newer version")
			component.Status.Component = *helper.NewTestComponentInfo(compOCMName, compNewVersion, sourceSpecData)
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
			sourcePath := Must(os.MkdirTemp("", sourcePattern))
			DeferCleanup(func() error {
				return os.RemoveAll(sourcePath)
			})

			helper.NewTestComponentVersionInCTFDir(env, sourcePath, compOCMName, compVersion, testImage)

			By("Create source repository resource")
			sourceRepo, sourceSpecData := helper.NewTestCFTRepository(testNamespace, sourceRepoResourceName, sourcePath)
			Expect(k8sClient.Create(ctx, sourceRepo)).To(Succeed())

			By("Simulate ocmrepository controller for source repository")
			conditions.MarkTrue(sourceRepo, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, sourceRepo)).To(Succeed())

			By("Create source component resource")
			component := helper.NewTestComponent(testNamespace, compResourceName, sourceRepoResourceName, compOCMName, compVersion)
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("Simulate component controller")
			component.Status.Component = *helper.NewTestComponentInfo(compOCMName, compVersion, sourceSpecData)
			conditions.MarkTrue(component, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, component)).To(Succeed())

			By("Create target CTF")
			targetPath := Must(os.MkdirTemp("", targetPattern))
			DeferCleanup(func() error {
				return os.RemoveAll(targetPath)
			})

			By("Create target repository resource")
			targetRepo, targetSpecData := helper.NewTestCFTRepository(testNamespace, targetRepoResourceName, targetPath)
			Expect(k8sClient.Create(ctx, targetRepo)).To(Succeed())

			By("Simulate ocmrepository controller for target repository")
			targetRepo.Spec.RepositorySpec.Raw = *targetSpecData
			conditions.MarkTrue(targetRepo, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, targetRepo)).To(Succeed())

			By("Create ConfigMap with transfer options")
			configMap := helper.NewTestConfigMapForData(testNamespace, optResourceName, helper.OCMConfigResourcesByValue)
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			By("Create and reconcile Replication resource")
			replication := helper.NewTestReplication(testNamespace, replResourceName, compResourceName, targetRepoResourceName)
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
	})
})

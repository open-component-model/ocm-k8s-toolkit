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
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/mandelsoft/goutils/testutils"
	"github.com/mandelsoft/vfs/pkg/osfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	. "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"
	ocmmetav1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
	resourcetypes "ocm.software/ocm/api/ocm/extensions/artifacttypes"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/utils/accessio"
)

var _ = Describe("Replication Controller", func() {
	Context("When reconciling a Replication", func() {
		const (
			replResourceName       = "test-replication"
			compResourceName       = "test-component"
			sourceRepoResourceName = "test-source-repository"
			targetRepoResourceName = "test-target-repository"
			testNamespace          = "ns-test-replication-controller"

			compOCMName = "ocm.software/component-for-replication"
			compVersion = "0.1.0"
		)

		var (
			ctx       context.Context
			cancel    context.CancelFunc
			namespace *corev1.Namespace
			env       *Builder
		)

		replNamespacedName := types.NamespacedName{
			Name:      replResourceName,
			Namespace: testNamespace,
		}

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
		})

		AfterEach(func() {
		})

		It("Default transfer operation from one CTF to another: no explicit transfer options, no credentials.", func() {
			By("Create source CTF")
			sourcePattern := "ocm-k8s-replication-source--*"
			sourcePath := Must(os.MkdirTemp("", sourcePattern))
			DeferCleanup(func() error {
				return os.RemoveAll(sourcePath)
			})

			env.OCMCommonTransport(sourcePath, accessio.FormatDirectory, func() {
				env.Component(compOCMName, func() {
					env.Version(compVersion, func() {
						env.Resource("image", "1.0.0", resourcetypes.OCI_IMAGE, ocmmetav1.ExternalRelation, func() {
							env.Access(
								ociartifact.New("gcr.io/google_containers/echoserver:1.10"),
							)
						})
					})
				})
			})

			By("Create source repository resource")
			sourceSpec := Must(ctf.NewRepositorySpec(ctf.ACC_READONLY, sourcePath))
			sourceSpecData := Must(sourceSpec.MarshalJSON())
			sourceRepo := &v1alpha1.OCMRepository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      sourceRepoResourceName,
				},
				Spec: v1alpha1.OCMRepositorySpec{
					RepositorySpec: &apiextensionsv1.JSON{
						Raw: sourceSpecData,
					},
					Interval: metav1.Duration{Duration: time.Minute * 10},
				},
			}
			Expect(k8sClient.Create(ctx, sourceRepo)).To(Succeed())

			By("Simulate ocmrepository controller for source repository")
			conditions.MarkTrue(sourceRepo, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, sourceRepo)).To(Succeed())

			By("Create source component resource")
			component := &v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      compResourceName,
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: testNamespace,
						Name:      sourceRepoResourceName,
					},
					Component: compOCMName,
					Semver:    compVersion,
					Interval:  metav1.Duration{Duration: time.Minute * 10},
				},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("Simulate component controller")
			component.Status.Component = v1alpha1.ComponentInfo{
				RepositorySpec: &apiextensionsv1.JSON{Raw: sourceSpecData},
				Component:      compOCMName,
				Version:        compVersion,
			}
			conditions.MarkTrue(component, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, component)).To(Succeed())

			By("Create target CTF")
			targetPattern := "ocm-k8s-replication-target--*"
			targetPath := Must(os.MkdirTemp("", targetPattern))
			DeferCleanup(func() error {
				return os.RemoveAll(targetPath)
			})

			By("Create target repository resource")
			targetSpec := Must(ctf.NewRepositorySpec(ctf.ACC_CREATE, targetPath))
			targetSpecData := Must(targetSpec.MarshalJSON())
			targetRepo := &v1alpha1.OCMRepository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      targetRepoResourceName,
				},
				Spec: v1alpha1.OCMRepositorySpec{
					RepositorySpec: &apiextensionsv1.JSON{
						Raw: targetSpecData,
					},
					Interval: metav1.Duration{Duration: time.Minute * 10},
				},
			}
			Expect(k8sClient.Create(ctx, targetRepo)).To(Succeed())

			By("Simulate ocmrepository controller for target repository")
			targetRepo.Spec.RepositorySpec.Raw = targetSpecData
			conditions.MarkTrue(targetRepo, meta.ReadyCondition, "ready", "")
			Expect(k8sClient.Status().Update(ctx, targetRepo)).To(Succeed())

			By("Create and reconcile Replication resource")
			replication := &v1alpha1.Replication{
				ObjectMeta: metav1.ObjectMeta{
					Name:      replResourceName,
					Namespace: testNamespace,
				},
				Spec: v1alpha1.ReplicationSpec{
					ComponentRef: v1alpha1.ObjectKey{
						Name:      compResourceName,
						Namespace: testNamespace,
					},
					TargetRepositoryRef: v1alpha1.ObjectKey{
						Name:      targetRepoResourceName,
						Namespace: testNamespace,
					},
				},
			}
			Expect(k8sClient.Create(ctx, replication)).To(Succeed())

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, replNamespacedName, replication)).To(Succeed())
				return conditions.IsReady(replication)
			}).WithTimeout(10 * time.Second).Should(BeTrue())

			By("Cleanup the resources")
			Expect(k8sClient.Delete(ctx, replication)).To(Succeed())
			Expect(k8sClient.Delete(ctx, targetRepo)).To(Succeed())
			Expect(k8sClient.Delete(ctx, component)).To(Succeed())
			Expect(k8sClient.Delete(ctx, sourceRepo)).To(Succeed())
		})
	})
})

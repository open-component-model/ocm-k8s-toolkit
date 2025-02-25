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

package component

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	. "github.com/mandelsoft/goutils/testutils"
	"github.com/mandelsoft/vfs/pkg/vfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	. "ocm.software/ocm/api/helper/builder"
	"ocm.software/ocm/api/utils/accessobj"
	"sigs.k8s.io/yaml"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/mandelsoft/vfs/pkg/osfs"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	environment "ocm.software/ocm/api/helper/env"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/utils/accessio"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
)

const (
	CTFPath      = "ocm-k8s-ctfstore--*"
	Component    = "ocm.software/test-component"
	ComponentObj = "test-component"
	Version1     = "1.0.0"
	Version2     = "1.0.1"
)

var _ = Describe("Component Controller", func() {
	var (
		env     *Builder
		ctfpath string
	)
	BeforeEach(func() {
		ctfpath = Must(os.MkdirTemp("", CTFPath))
		env = NewBuilder(environment.FileSystem(osfs.OsFs))
	})
	AfterEach(func() {
		Expect(os.RemoveAll(ctfpath)).To(Succeed())
		Expect(env.Cleanup()).To(Succeed())
	})

	Context("component controller", func() {
		var repositoryObj *v1alpha1.OCMRepository
		var namespace *corev1.Namespace

		BeforeEach(func(ctx SpecContext) {
			By("creating a repository with name")
			env.OCMCommonTransport(ctfpath, accessio.FormatDirectory, func() {
				env.Component(Component, func() {
					env.Version(Version1)
				})
			})

			spec := Must(ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfpath))
			specdata := Must(spec.MarshalJSON())

			namespaceName := generateNamespace(ctx.SpecReport().LeafNodeText)
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

			repositoryName := "repository"
			repositoryObj = &v1alpha1.OCMRepository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespaceName,
					Name:      repositoryName,
				},
				Spec: v1alpha1.OCMRepositorySpec{
					RepositorySpec: &apiextensionsv1.JSON{
						Raw: specdata,
					},
					Interval: metav1.Duration{Duration: time.Minute * 10},
				},
			}
			Expect(k8sClient.Create(ctx, repositoryObj)).To(Succeed())

			conditions.MarkTrue(repositoryObj, "Ready", "ready", "message")
			Expect(k8sClient.Status().Update(ctx, repositoryObj)).To(Succeed())
		})

		AfterEach(func(ctx SpecContext) {
			// make sure the repo is still ready
			conditions.MarkTrue(repositoryObj, "Ready", "ready", "message")
			Expect(k8sClient.Status().Update(ctx, repositoryObj)).To(Succeed())

			By("deleting the repository")
			Expect(k8sClient.Delete(ctx, repositoryObj)).To(Succeed())
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(repositoryObj), repositoryObj)
				return errors.IsNotFound(err)
			}).WithContext(ctx).Should(BeTrue())

			components := &v1alpha1.ComponentList{}

			Expect(k8sClient.List(ctx, components, client.InNamespace(namespace.GetName()))).To(Succeed())
			Expect(components.Items).To(HaveLen(0))

			snapshots := &v1alpha1.SnapshotList{}
			Expect(k8sClient.List(ctx, snapshots, client.InNamespace(namespace.GetName()))).To(Succeed())
			Expect(snapshots.Items).To(HaveLen(0))
		})

		It("reconcileComponent a component", func(ctx SpecContext) {
			By("creating a component")
			component := &v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.GetName(),
					Name:      ComponentObj,
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: namespace.GetName(),
						Name:      repositoryObj.GetName(),
					},
					Component: Component,
					Semver:    "1.0.0",
					Interval:  metav1.Duration{Duration: time.Minute * 10},
				},
				Status: v1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("checking that the component has been reconciled successfully")
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)
				if err != nil {
					return false
				}
				return conditions.IsReady(component)
			}, "15s").WithContext(ctx).Should(BeTrue())

			By("checking that the snapshot has been created successfully")
			Eventually(komega.Object(component), "15s").Should(
				HaveField("Status.SnapshotRef.Name", Not(BeEmpty())))
			snapshot := &v1alpha1.Snapshot{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: component.GetNamespace(), Name: component.GetSnapshotName()}, snapshot)).To(Succeed())

			By("validating the snapshot")
			ownersReference := snapshot.GetOwnerReferences()
			Expect(len(ownersReference)).To(Equal(1), "expected only one ownersReference")
			Expect(ownersReference[0].Name).To(Equal(component.GetName()), "expected to be a ownersReference of the component")

			By("checking that the snapshot contains the correct content")
			snapshotRepository := Must(registry.NewRepository(ctx, snapshot.Spec.Repository))
			snapshotComponentContent := Must(snapshotRepository.FetchSnapshot(ctx, snapshot.GetDigest()))

			snapshotDescriptors := &ocm.Descriptors{}
			MustBeSuccessful(yaml.Unmarshal(snapshotComponentContent, snapshotDescriptors))
			repo := Must(ctf.Open(env, accessobj.ACC_WRITABLE, ctfpath, vfs.FileMode(vfs.O_RDWR), env))
			cv := Must(repo.LookupComponentVersion(Component, Version1))
			expectedDescriptors := Must(ocm.ListComponentDescriptors(ctx, cv, repo))
			Expect(snapshotDescriptors).To(YAMLEqual(expectedDescriptors))

			By("delete resources manually")
			Expect(k8sClient.Delete(ctx, component)).To(Succeed())
			By("checking that the component has been deleted successfully")
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)
				return errors.IsNotFound(err)
			}, "15s").WithContext(ctx).Should(BeTrue())
			By("checking that the snapshot has been deleted successfully")
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(snapshot), snapshot)
				return errors.IsNotFound(err)
			}, "15s").WithContext(ctx).Should(BeTrue())
		})

		It("does not reconcile when the repository is not ready", func(ctx SpecContext) {
			By("marking the repository as not ready")
			conditions.MarkFalse(repositoryObj, "Ready", "notReady", "reason")
			Expect(k8sClient.Status().Update(ctx, repositoryObj)).To(Succeed())

			By("creating a component object")
			component := &v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.GetName(),
					Name:      ComponentObj,
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: namespace.GetName(),
						Name:      repositoryObj.GetName(),
					},
					Component: Component,
					Semver:    "1.0.0",
					Interval:  metav1.Duration{Duration: time.Minute * 10},
				},
				Status: v1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("checking that the component has not been reconciled successfully")
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)
				if err != nil {
					return false
				}

				return !conditions.IsReady(component)
			}, "15s").WithContext(ctx).Should(BeTrue())

			By("checking that the snapshot has not been created successfully")
			Expect(component).To(HaveField("Status.SnapshotRef.Name", BeEmpty()))

			By("deleting the resources manually")
			Expect(k8sClient.Delete(ctx, component)).To(Succeed())
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)

				return errors.IsNotFound(err)
			}, "15s").WithContext(ctx).Should(BeTrue())
		})

		It("grabs the new version when it becomes available", func(ctx SpecContext) {
			By("creating a component")
			component := &v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.GetName(),
					Name:      ComponentObj,
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: namespace.GetName(),
						Name:      repositoryObj.GetName(),
					},
					Component: Component,
					Semver:    ">=1.0.0",
					Interval:  metav1.Duration{Duration: time.Second},
				},
				Status: v1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("checking that the component has been reconciled successfully")
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)
				if err != nil {
					return false
				}
				return conditions.IsReady(component)
			}, "15s").WithContext(ctx).Should(BeTrue())

			By("checking that the snapshot has been created successfully")
			Eventually(komega.Object(component), "15s").Should(
				HaveField("Status.SnapshotRef.Name", Not(BeEmpty())))
			snapshot := &v1alpha1.Snapshot{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: component.GetNamespace(), Name: component.GetSnapshotName()}, snapshot)).To(Succeed())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())
			Expect(component.Status.Component.Version).To(Equal(Version1))

			env.OCMCommonTransport(ctfpath, accessio.FormatDirectory, func() {
				env.Component(Component, func() {
					env.Version(Version1)
				})
				env.Component(Component, func() {
					env.Version(Version2)
				})
			})

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())

				return component.Status.Component.Version == Version2
			}).WithTimeout(15 * time.Second).Should(BeTrue())

			By("delete resources manually")
			Expect(k8sClient.Delete(ctx, component)).To(Succeed())
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)
				return errors.IsNotFound(err)
			}, "15s").WithContext(ctx).Should(BeTrue())
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(snapshot), snapshot)
				return errors.IsNotFound(err)
			}, "15s").WithContext(ctx).Should(BeTrue())
		})

		It("grabs lower version if downgrade is allowed", func(ctx SpecContext) {
			componentName := Component + "-downgrade"
			env.OCMCommonTransport(ctfpath, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version("0.0.3", func() {
						env.Label(v1alpha1.OCMLabelDowngradable, "0.0.2")
					})
					env.Version("0.0.2", func() {
						env.Label(v1alpha1.OCMLabelDowngradable, "0.0.2")
					})
				})
			})

			By("creating a component")
			component := &v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.GetName(),
					Name:      ComponentObj,
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: namespace.GetName(),
						Name:      repositoryObj.GetName(),
					},
					Component:       componentName,
					DowngradePolicy: v1alpha1.DowngradePolicyAllow,
					Semver:          "<1.0.0",
					Interval:        metav1.Duration{Duration: time.Second},
				},
				Status: v1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("checking that the component has been reconciled successfully")
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)
				if err != nil {
					return false
				}
				return conditions.IsReady(component)
			}, "15s").WithContext(ctx).Should(BeTrue())

			By("checking that the snapshot has been created successfully")
			Eventually(komega.Object(component), "15s").Should(
				HaveField("Status.SnapshotRef.Name", Not(BeEmpty())))
			snapshot := &v1alpha1.Snapshot{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: component.GetNamespace(), Name: component.GetSnapshotName()}, snapshot)).To(Succeed())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())
			Expect(component.Status.Component.Version).To(Equal("0.0.3"))

			component.Spec.Semver = "0.0.2"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())

				return component.Status.Component.Version == "0.0.2"
			}).WithTimeout(15 * time.Second).Should(BeTrue())

			By("delete resources manually")
			Expect(k8sClient.Delete(ctx, component)).To(Succeed())
			By("verifying component is deleted")
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)
				return errors.IsNotFound(err)
			}, "15s").WithContext(ctx).Should(BeTrue())
			By("verifying snapshot is deleted")
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(snapshot), snapshot)
				return errors.IsNotFound(err)
			}, "15s").WithContext(ctx).Should(BeTrue())
		})

		It("does not grab lower version if downgrade is denied", func(ctx SpecContext) {
			componentName := Component + "-downgrade-2"
			env.OCMCommonTransport(ctfpath, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version("0.0.3", func() {
						env.Label(v1alpha1.OCMLabelDowngradable, "0.0.2")
					})
					env.Version("0.0.2", func() {
						env.Label(v1alpha1.OCMLabelDowngradable, "0.0.2")
					})
				})
			})

			By("creating a component")
			component := &v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.GetName(),
					Name:      ComponentObj,
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: namespace.GetName(),
						Name:      repositoryObj.GetName(),
					},
					Component:       componentName,
					DowngradePolicy: v1alpha1.DowngradePolicyDeny,
					Semver:          "0.0.3",
					Interval:        metav1.Duration{Duration: time.Second},
				},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("checking that the component has been reconciled successfully")
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)
				if err != nil {
					return false
				}
				return conditions.IsReady(component)
			}, "15s").WithContext(ctx).Should(BeTrue())

			By("checking that the snapshot has been created successfully")
			Eventually(komega.Object(component), "15s").Should(
				HaveField("Status.SnapshotRef.Name", Not(BeEmpty())))
			snapshotComponent := &v1alpha1.Snapshot{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: component.GetNamespace(), Name: component.GetSnapshotName()}, snapshotComponent)).To(Succeed())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())
			Expect(component.Status.Component.Version).To(Equal("0.0.3"))

			component.Spec.Semver = "0.0.2"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())

				cond := conditions.Get(component, meta.ReadyCondition)
				return cond.Message == "terminal error: component version cannot be downgraded from version 0.0.3 to version 0.0.2"
			}).WithTimeout(15 * time.Second).Should(BeTrue())

			By("delete resources manually")
			Expect(k8sClient.Delete(ctx, component)).To(Succeed())
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)
				return errors.IsNotFound(err)
			}, "15s").WithContext(ctx).Should(BeTrue())
		})

		It("can force downgrade even if not allowed by the component", func(ctx SpecContext) {
			componentName := Component + "-downgrade-3"
			env.OCMCommonTransport(ctfpath, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version("0.0.3")
					env.Version("0.0.2")
				})
			})

			By("creating a component")
			component := &v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.GetName(),
					Name:      ComponentObj,
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: namespace.GetName(),
						Name:      repositoryObj.GetName(),
					},
					Component:       componentName,
					DowngradePolicy: v1alpha1.DowngradePolicyEnforce,
					Semver:          "<1.0.0",
					Interval:        metav1.Duration{Duration: time.Second},
				},
				Status: v1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("checking that the component has been reconciled successfully")
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)
				if err != nil {
					return false
				}
				return conditions.IsReady(component)
			}, "15s").WithContext(ctx).Should(BeTrue())

			By("checking that the snapshot has been created successfully")
			Eventually(komega.Object(component), "15s").Should(
				HaveField("Status.SnapshotRef.Name", Not(BeEmpty())))
			snapshot := &v1alpha1.Snapshot{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: component.GetNamespace(), Name: component.GetSnapshotName()}, snapshot)).To(Succeed())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())
			Expect(component.Status.Component.Version).To(Equal("0.0.3"))

			component.Spec.Semver = "0.0.2"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())

				return component.Status.Component.Version == "0.0.2"
			}).WithTimeout(60 * time.Second).Should(BeTrue())

			By("delete resources manually")
			Expect(k8sClient.Delete(ctx, component)).To(Succeed())
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(snapshot), snapshot)
				return errors.IsNotFound(err)
			}, "15s").WithContext(ctx).Should(BeTrue())
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)
				return errors.IsNotFound(err)
			}, "15s").WithContext(ctx).Should(BeTrue())
		})
	})

	Context("ocm config handling", func() {
		var (
			configs       []*corev1.ConfigMap
			secrets       []*corev1.Secret
			namespace     *corev1.Namespace
			repositoryObj *v1alpha1.OCMRepository
		)

		BeforeEach(func(ctx SpecContext) {
			By("creating a repository with name")
			env.OCMCommonTransport(ctfpath, accessio.FormatDirectory, func() {
				env.Component(Component, func() {
					env.Version(Version1)
				})
			})

			spec := Must(ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfpath))
			specdata := Must(spec.MarshalJSON())

			namespaceName := generateNamespace(ctx.SpecReport().LeafNodeText)
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

			configs, secrets = createTestConfigsAndSecrets(ctx, namespace.GetName())

			repositoryName := "repository"
			repositoryObj = &v1alpha1.OCMRepository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.GetName(),
					Name:      repositoryName,
				},
				Spec: v1alpha1.OCMRepositorySpec{
					RepositorySpec: &apiextensionsv1.JSON{
						Raw: specdata,
					},
					OCMConfig: []v1alpha1.OCMConfiguration{
						{
							NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
								APIVersion: corev1.SchemeGroupVersion.String(),
								Kind:       "Secret",
								Name:       secrets[0].Name,
								Namespace:  secrets[0].Namespace,
							},
						},
						{
							NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
								APIVersion: corev1.SchemeGroupVersion.String(),
								Kind:       "Secret",
								Name:       secrets[1].Name,
							},
							Policy: v1alpha1.ConfigurationPolicyDoNotPropagate,
						},
						{
							NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
								Kind: "Secret",
								Name: secrets[2].Name,
							},
							Policy: v1alpha1.ConfigurationPolicyPropagate,
						},
						{
							NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
								APIVersion: corev1.SchemeGroupVersion.String(),
								Kind:       "ConfigMap",
								Name:       configs[0].Name,
								Namespace:  configs[1].Namespace,
							},
						},
						{
							NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
								APIVersion: corev1.SchemeGroupVersion.String(),
								Kind:       "ConfigMap",
								Name:       configs[1].Name,
							},
							Policy: v1alpha1.ConfigurationPolicyDoNotPropagate,
						},
						{
							NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
								Kind: "ConfigMap",
								Name: configs[2].Name,
							},
							Policy: v1alpha1.ConfigurationPolicyPropagate,
						},
					},
					Interval: metav1.Duration{Duration: time.Minute * 10},
				},
			}

			Expect(k8sClient.Create(ctx, repositoryObj)).To(Succeed())

			repositoryObj.Status = v1alpha1.OCMRepositoryStatus{
				EffectiveOCMConfig: []v1alpha1.OCMConfiguration{
					{
						NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
							APIVersion: corev1.SchemeGroupVersion.String(),
							Kind:       "Secret",
							Name:       secrets[0].Name,
							Namespace:  secrets[0].Namespace,
						},
						Policy: v1alpha1.ConfigurationPolicyDoNotPropagate,
					},
					{
						NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
							APIVersion: corev1.SchemeGroupVersion.String(),
							Kind:       "Secret",
							Name:       secrets[1].Name,
							Namespace:  secrets[1].Namespace,
						},
						Policy: v1alpha1.ConfigurationPolicyDoNotPropagate,
					},
					{
						NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
							APIVersion: corev1.SchemeGroupVersion.String(),
							Kind:       "Secret",
							Name:       secrets[2].Name,
							Namespace:  secrets[2].Namespace,
						},
						Policy: v1alpha1.ConfigurationPolicyPropagate,
					},
					{
						NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
							APIVersion: corev1.SchemeGroupVersion.String(),
							Kind:       "ConfigMap",
							Name:       configs[0].Name,
							Namespace:  configs[1].Namespace,
						},
						Policy: v1alpha1.ConfigurationPolicyDoNotPropagate,
					},
					{
						NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
							APIVersion: corev1.SchemeGroupVersion.String(),
							Kind:       "ConfigMap",
							Name:       configs[1].Name,
							Namespace:  secrets[1].Namespace,
						},
						Policy: v1alpha1.ConfigurationPolicyDoNotPropagate,
					},
					{
						NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
							APIVersion: corev1.SchemeGroupVersion.String(),
							Kind:       "ConfigMap",
							Name:       configs[2].Name,
							Namespace:  configs[2].Namespace,
						},
						Policy: v1alpha1.ConfigurationPolicyPropagate,
					},
				},
			}

			conditions.MarkTrue(repositoryObj, "Ready", "ready", "message")
			Expect(k8sClient.Status().Update(ctx, repositoryObj)).To(Succeed())
		})

		AfterEach(func(ctx SpecContext) {
			By("make sure the repo is still ready")
			conditions.MarkTrue(repositoryObj, "Ready", "ready", "message")
			Expect(k8sClient.Status().Update(ctx, repositoryObj)).To(Succeed())
			cleanupTestConfigsAndSecrets(ctx, configs, secrets)

			By("delete repository")
			Expect(k8sClient.Delete(ctx, repositoryObj)).To(Succeed())
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(repositoryObj), repositoryObj)
				return errors.IsNotFound(err)
			}, "15s").WithContext(ctx).Should(BeTrue())

			By("ensuring no components are left")
			Eventually(func(g Gomega, ctx SpecContext) {
				components := &v1alpha1.ComponentList{}
				g.Expect(k8sClient.List(ctx, components, client.InNamespace(namespace.GetName()))).To(Succeed())
				g.Expect(components.Items).To(HaveLen(0))
			}, "15s").WithContext(ctx).Should(Succeed())
			By("ensuring no snapshots are left")
			Eventually(func(g Gomega, ctx SpecContext) {
				snapshots := &v1alpha1.SnapshotList{}
				g.Expect(k8sClient.List(ctx, snapshots, client.InNamespace(namespace.GetName()))).To(Succeed())
				g.Expect(snapshots.Items).To(HaveLen(0))
			}, "15s").WithContext(ctx).Should(Succeed())
		})

		It("component resolves and propagates config from repository", func(ctx SpecContext) {
			By("creating a component")
			component := &v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.GetName(),
					Name:      ComponentObj,
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: namespace.GetName(),
						Name:      repositoryObj.GetName(),
					},
					Component: Component,
					Semver:    "1.0.0",
					OCMConfig: []v1alpha1.OCMConfiguration{
						{
							NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
								APIVersion: v1alpha1.GroupVersion.String(),
								Kind:       v1alpha1.KindOCMRepository,
								Namespace:  namespace.GetName(),
								Name:       repositoryObj.GetName(),
							},
							Policy: v1alpha1.ConfigurationPolicyDoNotPropagate,
						},
					},
					Interval: metav1.Duration{Duration: time.Minute * 10},
				},
				Status: v1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("checking that the component has been reconciled successfully")
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)
				if err != nil {
					return false
				}
				return conditions.IsReady(component)
			}, "15s").WithContext(ctx).Should(BeTrue())

			By("checking that the snapshot has been created successfully")
			Eventually(komega.Object(component), "15s").Should(
				HaveField("Status.SnapshotRef.Name", Not(BeEmpty())))
			snapshot := &v1alpha1.Snapshot{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: component.GetNamespace(), Name: component.GetSnapshotName()}, snapshot)).To(Succeed())

			Eventually(komega.Object(component), "15s").Should(
				HaveField("Status.EffectiveOCMConfig", ConsistOf(
					v1alpha1.OCMConfiguration{
						NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
							APIVersion: corev1.SchemeGroupVersion.String(),
							Kind:       "Secret",
							Name:       secrets[2].Name,
							Namespace:  secrets[2].Namespace,
						},
						Policy: v1alpha1.ConfigurationPolicyDoNotPropagate,
					},
					v1alpha1.OCMConfiguration{
						NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
							APIVersion: corev1.SchemeGroupVersion.String(),
							Kind:       "ConfigMap",
							Name:       configs[2].Name,
							Namespace:  configs[2].Namespace,
						},
						Policy: v1alpha1.ConfigurationPolicyDoNotPropagate,
					},
				)),
			)

			By("delete resources manually")
			Expect(k8sClient.Delete(ctx, component)).To(Succeed())
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(snapshot), snapshot)
				return errors.IsNotFound(err)
			}, "15s").WithContext(ctx).Should(BeTrue())
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(component), component)
				return errors.IsNotFound(err)
			}, "15s").WithContext(ctx).Should(BeTrue())
		})
	})
})

func createTestConfigsAndSecrets(ctx context.Context, namespace string) (configs []*corev1.ConfigMap, secrets []*corev1.Secret) {
	const (
		Config1 = "config1"
		Config2 = "config2"
		Config3 = "config3"

		Secret1 = "secret1"
		Secret2 = "secret2"
		Secret3 = "secret3"
	)

	By("setup configs")
	config1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      Config1,
		},
		Data: map[string]string{
			v1alpha1.OCMConfigKey: `
type: generic.config.ocm.software/v1
sets:
  set1:
    description: set1
    configurations:
    - type: credentials.config.ocm.software
      consumers:
      - identity:
          type: MavenRepository
          hostname: example.com
          pathprefix: path/ocm
        credentials:
        - type: Credentials
          properties:
            username: testuser1
            password: testpassword1 
`,
		},
	}
	configs = append(configs, config1)
	Expect(k8sClient.Create(ctx, config1)).To(Succeed())

	config2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      Config2,
		},
		Data: map[string]string{
			v1alpha1.OCMConfigKey: `
type: generic.config.ocm.software/v1
sets:
  set2:
    description: set2
    configurations:
    - type: credentials.config.ocm.software
      consumers:
      - identity:
          type: MavenRepository
          hostname: example.com
          pathprefix: path/ocm
        credentials:
        - type: Credentials
          properties:
            username: testuser1
            password: testpassword1 
`,
		},
	}
	configs = append(configs, config2)
	Expect(k8sClient.Create(ctx, config2)).To(Succeed())

	config3 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      Config3,
		},
		Data: map[string]string{
			v1alpha1.OCMConfigKey: `
type: generic.config.ocm.software/v1
sets:
  set3:
    description: set3
    configurations:
    - type: credentials.config.ocm.software
      consumers:
      - identity:
          type: MavenRepository
          hostname: example.com
          pathprefix: path/ocm
        credentials:
        - type: Credentials
          properties:
            username: testuser1
            password: testpassword1 
`,
		},
	}
	configs = append(configs, config3)
	Expect(k8sClient.Create(ctx, config3)).To(Succeed())

	By("setup secrets")
	secret1 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      Secret1,
		},
		Data: map[string][]byte{
			v1alpha1.OCMConfigKey: []byte(`
type: credentials.config.ocm.software
consumers:
- identity:
    type: MavenRepository
    hostname: example.com
    pathprefix: path1
  credentials:
  - type: Credentials
    properties:
      username: testuser1
      password: testpassword1
`),
		},
	}
	secrets = append(secrets, secret1)
	Expect(k8sClient.Create(ctx, secret1)).To(Succeed())

	secret2 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      Secret2,
		},
		Data: map[string][]byte{
			v1alpha1.OCMConfigKey: []byte(`
type: credentials.config.ocm.software
consumers:
- identity:
    type: MavenRepository
    hostname: example.com
    pathprefix: path2
  credentials:
  - type: Credentials
    properties:
      username: testuser2
      password: testpassword2
`),
		},
	}
	secrets = append(secrets, secret2)
	Expect(k8sClient.Create(ctx, secret2)).To(Succeed())

	secret3 := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      Secret3,
		},
		Data: map[string][]byte{
			v1alpha1.OCMConfigKey: []byte(`
type: credentials.config.ocm.software
consumers:
- identity:
    type: MavenRepository
    hostname: example.com
    pathprefix: path3
  credentials:
  - type: Credentials
    properties:
      username: testuser3
      password: testpassword3
`),
		},
	}
	secrets = append(secrets, &secret3)
	Expect(k8sClient.Create(ctx, &secret3)).To(Succeed())

	return configs, secrets
}

func cleanupTestConfigsAndSecrets(ctx context.Context, configs []*corev1.ConfigMap, secrets []*corev1.Secret) {
	for _, config := range configs {
		Expect(k8sClient.Delete(ctx, config)).To(Succeed())
	}
	for _, secret := range secrets {
		Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
	}
}

func generateNamespace(testName string) string {
	replaced := strings.ToLower(strings.Replace(testName, " ", "-", -1))
	if len(replaced) > 63 {
		return replaced[:63]
	}
	return replaced
}

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
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/tar"
	"github.com/mandelsoft/filepath/pkg/filepath"
	. "github.com/mandelsoft/goutils/testutils"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	. "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/utils/accessio"
	"ocm.software/ocm/api/utils/accessobj"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/yaml"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
)

const (
	CTFPath       = "ocm-k8s-ctfstore--*"
	Namespace     = "test-namespace"
	RepositoryObj = "test-repository"
	Component     = "ocm.software/test-component"
	ComponentObj  = "test-component"
	Version1      = "1.0.0"
	Version2      = "1.0.1"
)

var _ = Describe("Component Controller", func() {
	var (
		ctx     context.Context
		cancel  context.CancelFunc
		env     *Builder
		ctfpath string
	)
	BeforeEach(func() {
		ctfpath = Must(os.MkdirTemp("", CTFPath))
		env = NewBuilder(environment.FileSystem(osfs.OsFs))
		ctx = context.Background()
		ctx, cancel = context.WithCancel(context.Background())
	})
	AfterEach(func() {
		Expect(os.RemoveAll(ctfpath)).To(Succeed())
		Expect(env.Cleanup()).To(Succeed())
		cancel()
	})

	Context("component controller", func() {
		var (
			repositoryName string
			testNumber     int
			repositoryObj  *v1alpha1.OCMRepository
		)
		BeforeEach(func() {
			By("creating a repository with name")
			env.OCMCommonTransport(ctfpath, accessio.FormatDirectory, func() {
				env.Component(Component, func() {
					env.Version(Version1)
				})
			})

			spec := Must(ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfpath))
			specdata := Must(spec.MarshalJSON())

			repositoryName = fmt.Sprintf("%s-%d", RepositoryObj, testNumber)
			repositoryObj = &v1alpha1.OCMRepository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: Namespace,
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

			testNumber++
		})

		AfterEach(func() {
			// make sure the repo is still ready
			conditions.MarkTrue(repositoryObj, "Ready", "ready", "message")
			Expect(k8sClient.Status().Update(ctx, repositoryObj)).To(Succeed())
		})

		It("reconcileComponent a component", func() {
			By("creating a component")
			component := &v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: Namespace,
					Name:      fmt.Sprintf("%s-%d", ComponentObj, testNumber),
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: Namespace,
						Name:      repositoryName,
					},
					Component: Component,
					Semver:    "1.0.0",
					Interval:  metav1.Duration{Duration: time.Minute * 10},
				},
				Status: v1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("check that artifact has been created successfully")

			Eventually(komega.Object(component), "15s").Should(
				HaveField("Status.ArtifactRef.Name", Not(BeEmpty())))

			artifact := &artifactv1.Artifact{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: component.Namespace,
					Name:      component.Status.ArtifactRef.Name,
				},
			}
			Eventually(komega.Get(artifact)).Should(Succeed())

			By("check if the component descriptor list can be retrieved from the artifact server")
			r := Must(http.Get(artifact.Spec.URL))

			tmpdir := Must(os.MkdirTemp("/tmp", "descriptors-"))
			DeferCleanup(func() error {
				return os.RemoveAll(tmpdir)
			})
			MustBeSuccessful(tar.Untar(r.Body, tmpdir))

			repo := Must(ctf.Open(env, accessobj.ACC_WRITABLE, ctfpath, vfs.FileMode(vfs.O_RDWR), env))
			cv := Must(repo.LookupComponentVersion(Component, Version1))
			expecteddescs := Must(ocm.ListComponentDescriptors(ctx, cv, repo))

			data := Must(os.ReadFile(filepath.Join(tmpdir, v1alpha1.OCMComponentDescriptorList)))
			descs := &ocm.Descriptors{}
			MustBeSuccessful(yaml.Unmarshal(data, descs))
			Expect(descs).To(YAMLEqual(expecteddescs))
			Expect(component.Status.Artifact).To(Equal(artifact.Spec))
		})

		It("does not reconcile when the repository is not ready", func() {
			By("marking the repository as not ready")
			conditions.MarkFalse(repositoryObj, "Ready", "notReady", "reason")
			Expect(k8sClient.Status().Update(ctx, repositoryObj)).To(Succeed())

			By("creating a component object")
			component := &v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: Namespace,
					Name:      ComponentObj + "-not-ready",
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: Namespace,
						Name:      repositoryName,
					},
					Component: Component,
					Semver:    "1.0.0",
					Interval:  metav1.Duration{Duration: time.Minute * 10},
				},
				Status: v1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("check that no artifact has been created")
			Eventually(komega.Object(component), "15s").Should(
				HaveField("Status.ArtifactRef.Name", BeEmpty()))
		})

		It("grabs the new version when it becomes available", func() {
			By("creating a component")
			component := &v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: Namespace,
					Name:      fmt.Sprintf("%s-%d", ComponentObj, testNumber),
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: Namespace,
						Name:      repositoryName,
					},
					Component: Component,
					Semver:    ">=1.0.0",
					Interval:  metav1.Duration{Duration: time.Second},
				},
				Status: v1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("check that artifact has been created successfully")

			Eventually(komega.Object(component), "15s").Should(
				HaveField("Status.ArtifactRef.Name", Not(BeEmpty())))

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
		})

		It("grabs lower version if downgrade is allowed", func() {
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
					Namespace: Namespace,
					Name:      fmt.Sprintf("%s-%d", ComponentObj, testNumber),
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: Namespace,
						Name:      repositoryName,
					},
					Component:       componentName,
					DowngradePolicy: v1alpha1.DowngradeAllow,
					Semver:          "<1.0.0",
					Interval:        metav1.Duration{Duration: time.Second},
				},
				Status: v1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("check that artifact has been created successfully")

			Eventually(komega.Object(component), "15s").Should(HaveField("Status.ArtifactRef.Name", Not(BeEmpty())))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())
			Expect(component.Status.Component.Version).To(Equal("0.0.3"))

			component.Spec.Semver = "0.0.2"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())

				return component.Status.Component.Version == "0.0.2"
			}).WithTimeout(15 * time.Second).Should(BeTrue())
		})

		It("does not grab lower version if downgrade is denied", func() {
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
					Namespace: Namespace,
					Name:      fmt.Sprintf("%s-%d", ComponentObj, testNumber),
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: Namespace,
						Name:      repositoryName,
					},
					Component:       componentName,
					DowngradePolicy: v1alpha1.DowngradeDeny,
					Semver:          "0.0.3",
					Interval:        metav1.Duration{Duration: time.Second},
				},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("check that artifact has been created successfully")
			Eventually(komega.Object(component), "15s").Should(HaveField("Status.ArtifactRef.Name", Not(BeEmpty())))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())
			Expect(component.Status.Component.Version).To(Equal("0.0.3"))

			component.Spec.Semver = "0.0.2"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())

				cond := conditions.Get(component, meta.ReadyCondition)
				return cond.Message == "component version cannot be downgraded from version 0.0.3 to version 0.0.2"
			}).WithTimeout(15 * time.Second).Should(BeTrue())
		})

		It("can force downgrade even if not allowed by the component", func() {
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
					Namespace: Namespace,
					Name:      fmt.Sprintf("%s-%d", ComponentObj, testNumber),
				},
				Spec: v1alpha1.ComponentSpec{
					RepositoryRef: v1alpha1.ObjectKey{
						Namespace: Namespace,
						Name:      repositoryName,
					},
					Component:       componentName,
					DowngradePolicy: v1alpha1.DowngradeEnforce,
					Semver:          "<1.0.0",
					Interval:        metav1.Duration{Duration: time.Second},
				},
				Status: v1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("check that artifact has been created successfully")

			Eventually(komega.Object(component), "15s").Should(
				HaveField("Status.ArtifactRef.Name", Not(BeEmpty())))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())
			Expect(component.Status.Component.Version).To(Equal("0.0.3"))

			component.Spec.Semver = "0.0.2"
			Expect(k8sClient.Update(ctx, component)).To(Succeed())

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, component)).To(Succeed())

				return component.Status.Component.Version == "0.0.2"
			}).WithTimeout(15 * time.Second).Should(BeTrue())
		})
	})
})

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

	. "github.com/mandelsoft/goutils/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "ocm.software/ocm/api/helper/builder"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/tar"
	"github.com/mandelsoft/filepath/pkg/filepath"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	"k8s.io/apimachinery/pkg/types"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/utils/accessio"
	"ocm.software/ocm/api/utils/accessobj"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/yaml"

	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	environment "ocm.software/ocm/api/helper/env"

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

		repositoryName string
		testNumber     int
		repositoryObj  *v1alpha1.OCMRepository
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
					DowngradePolicy: v1alpha1.DowngradePolicyAllow,
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
					DowngradePolicy: v1alpha1.DowngradePolicyDeny,
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
				return cond.Message == "terminal error: component version cannot be downgraded from version 0.0.3 to version 0.0.2"
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
					DowngradePolicy: v1alpha1.DowngradePolicyEnforce,
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

	Context("ocm config handling", func() {
		const (
			Namespace = "test-namespace"
		)

		var (
			configs []*corev1.ConfigMap
			secrets []*corev1.Secret
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

			configs, secrets = createTestConfigsAndSecrets(ctx)

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

			testNumber++
		})

		AfterEach(func() {
			// make sure the repo is still ready
			conditions.MarkTrue(repositoryObj, "Ready", "ready", "message")
			Expect(k8sClient.Status().Update(ctx, repositoryObj)).To(Succeed())
			cleanupTestConfigsAndSecrets(ctx, configs, secrets)
		})

		It("component resolves and propagates config from repository", func() {
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
					OCMConfig: []v1alpha1.OCMConfiguration{
						{
							NamespacedObjectKindReference: meta.NamespacedObjectKindReference{
								APIVersion: v1alpha1.GroupVersion.String(),
								Kind:       v1alpha1.KindOCMRepository,
								Name:       repositoryName,
								Namespace:  Namespace,
							},
							Policy: v1alpha1.ConfigurationPolicyDoNotPropagate,
						},
					},
					Interval: metav1.Duration{Duration: time.Minute * 10},
				},
				Status: v1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

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
				)))
		})
	})
})

func createTestConfigsAndSecrets(ctx context.Context) (configs []*corev1.ConfigMap, secrets []*corev1.Secret) {
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
			Namespace: Namespace,
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
			Namespace: Namespace,
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
			Namespace: Namespace,
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
			Namespace: Namespace,
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
			Namespace: Namespace,
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
			Namespace: Namespace,
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

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

package resource

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/mandelsoft/vfs/pkg/osfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	. "ocm.software/ocm/api/helper/builder"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/github"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/helm"
	ocmociartifact "ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
	"ocm.software/ocm/api/ocm/extensions/artifacttypes"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/utils/accessio"
	"ocm.software/ocm/api/utils/mime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	environment "ocm.software/ocm/api/helper/env"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/test"
)

var _ = Describe("Resource Controller", func() {
	var (
		env *Builder
	)

	BeforeEach(func() {
		env = NewBuilder(environment.FileSystem(osfs.OsFs))
	})
	AfterEach(func() {
		Expect(env.Cleanup()).To(Succeed())
	})

	Context("resource controller", func() {
		var componentObj *v1alpha1.Component
		var namespace *corev1.Namespace
		var componentName, componentObjName, resourceName string
		var componentVersion string
		repositoryName := "ocm.software/test-repository"

		BeforeEach(func(ctx SpecContext) {
			componentObjName = test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			componentName = "ocm.software/test-component-" + test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			resourceName = "test-resource" + test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			componentVersion = "v1.0.0"

			namespaceName := test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		})

		AfterEach(func(ctx SpecContext) {
			By("deleting the component")
			Expect(k8sClient.Delete(ctx, componentObj)).To(Succeed())
			Eventually(func(ctx context.Context) error {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(componentObj), componentObj)
				if err != nil {
					if errors.IsNotFound(err) {
						return nil
					}
					return err
				}

				return fmt.Errorf("resource %s still exists", componentObj.Name)
			}).WithContext(ctx).Should(Succeed())

			resources := &v1alpha1.ResourceList{}
			Expect(k8sClient.List(ctx, resources, client.InNamespace(namespace.GetName()))).To(Succeed())
			Expect(resources.Items).To(HaveLen(0))
		})

		DescribeTable("should reconcile a created resource",
			func(createCTF func() string, expSourceRef *v1alpha1.SourceReference) {
				ctfPath := createCTF()

				spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfPath)
				Expect(err).NotTo(HaveOccurred())
				specData, err := spec.MarshalJSON()
				Expect(err).NotTo(HaveOccurred())

				By("mocking a component")
				componentObj = test.MockComponent(
					ctx,
					componentObjName,
					namespace.GetName(),
					&test.MockComponentOptions{
						Client:   k8sClient,
						Recorder: recorder,
						Info: v1alpha1.ComponentInfo{
							Component:      componentName,
							Version:        componentVersion,
							RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
						},
						Repository: repositoryName,
					},
				)

				By("creating a resourceObj")
				resourceObj := &v1alpha1.Resource{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: namespace.GetName(),
					},
					Spec: v1alpha1.ResourceSpec{
						ComponentRef: corev1.LocalObjectReference{
							Name: componentObj.GetName(),
						},
						Resource: v1alpha1.ResourceID{
							ByReference: v1alpha1.ResourceReference{
								Resource: v1.NewIdentity(resourceName),
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resourceObj)).To(Succeed())

				By("checking that the resourceObj has been reconciled successfully")
				waitUntilResourceIsReady(ctx, resourceObj)

				if expSourceRef != nil {
					Expect(resourceObj.Status.Reference).To(Equal(expSourceRef))
				}

				By("deleting the resource")
				deleteResource(ctx, resourceObj)

			},

			Entry("plain text", func() string {
				ctfPath := GinkgoT().TempDir()
				env.OCMCommonTransport(ctfPath, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(componentVersion, func() {
							env.Resource(resourceName, "1.0.0", artifacttypes.PLAIN_TEXT, v1.LocalRelation, func() {
								env.BlobData(mime.MIME_TEXT, []byte("Hello World!"))
							})
						})
					})
				})
				return ctfPath
			},
				nil),
			Entry("OCI artifact access", func() string {
				ctfPath := GinkgoT().TempDir()
				env.OCMCommonTransport(ctfPath, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(componentVersion, func() {
							env.Resource(resourceName, "1.0.0", artifacttypes.OCI_ARTIFACT, v1.ExternalRelation, func() {
								env.Access(ocmociartifact.New(fmt.Sprintf("ghcr.io/open-component-model/ocm/ocm.software/ocmcli/ocmcli-image:0.24.0")))
							})
						})
					})
				})
				return ctfPath
			},
				&v1alpha1.SourceReference{
					Registry:   "ghcr.io",
					Repository: "open-component-model/ocm/ocm.software/ocmcli/ocmcli-image",
					Tag:        "0.24.0",
				},
			),
			Entry("Helm access", func() string {
				ctfPath := GinkgoT().TempDir()
				env.OCMCommonTransport(ctfPath, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(componentVersion, func() {
							env.Resource(resourceName, "1.0.0", artifacttypes.HELM_CHART, v1.ExternalRelation, func() {
								env.Access(helm.New("podinfo:6.7.1", "oci://ghcr.io/stefanprodan/charts"))
							})
						})
					})
				})
				return ctfPath
			},
				&v1alpha1.SourceReference{
					Registry:   "oci://ghcr.io/stefanprodan/charts",
					Repository: "podinfo",
					Reference:  "6.7.1",
				},
			),
			Entry("GitHub access", func() string {
				ctfPath := GinkgoT().TempDir()
				env.OCMCommonTransport(ctfPath, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(componentVersion, func() {
							env.Resource(resourceName, "1.0.0", artifacttypes.DIRECTORY_TREE, v1.ExternalRelation, func() {
								env.Access(github.New(
									"https://github.com/open-component-model/ocm-k8s-toolkit",
									"/repos/open-component-model/ocm-k8s-toolkit",
									github.WithReference("main"),
								))
							})
						})
					})
				})
				return ctfPath
			},
				&v1alpha1.SourceReference{
					Registry:   "https://github.com",
					Repository: "/open-component-model/ocm-k8s-toolkit",
					Reference:  "main",
				},
			),
			// TODO: @frewilhelm potential bug in ocm
			//   Instead of creating the directory /tmp/repository, it tries to create /repository which is forbidden.
			//   see ocm/api/utils/blobaccess/git/access.go:24
			//          ocm/api/utils/blobaccess/git/access.go:51
			//Entry("git access", func() string {
			//	ctfPath := GinkgoT().TempDir()
			//	env.OCMCommonTransport(ctfPath, accessio.FormatDirectory, func() {
			//		env.Component(componentName, func() {
			//			env.Version(componentVersion, func() {
			//				env.Resource(resourceName, "1.0.0", artifacttypes.DIRECTORY_TREE, v1.ExternalRelation, func() {
			//					env.Access(git.New(
			//						"https://github.com/open-component-model/ocm-k8s-toolkit",
			//						git.WithRef("refs/heads/main"),
			//					))
			//				})
			//			})
			//		})
			//	})
			//	return ctfPath
			//},
			//	&v1alpha1.SourceReference{
			//		Registry:   "https://github.com",
			//		Repository: "/open-component-model/ocm-k8s-toolkit",
			//		Reference:  "refs/heads/main",
			//	},
			//),
		)
	})
})

func deleteResource(ctx context.Context, resource *v1alpha1.Resource) {
	GinkgoHelper()

	Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

	Eventually(func(ctx context.Context) error {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}

		return fmt.Errorf("resource %s still exists", resource.Name)
	}, "15s").WithContext(ctx).Should(Succeed())
}

func waitUntilResourceIsReady(ctx context.Context, resource *v1alpha1.Resource) {
	GinkgoHelper()

	Eventually(func(g Gomega, ctx context.Context) error {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)
		if err != nil {
			return err
		}
		g.Expect(resource).Should(HaveField("Status.Resource", Not(BeNil())))

		if !conditions.IsReady(resource) {
			return fmt.Errorf("resource %s is not ready", resource.Name)
		}

		return nil
	}, "15s").WithContext(ctx).Should(Succeed())
}

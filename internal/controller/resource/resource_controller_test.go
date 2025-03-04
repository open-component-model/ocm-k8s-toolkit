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
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"

	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/mandelsoft/goutils/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	. "ocm.software/ocm/api/helper/builder"
	"ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
	"ocm.software/ocm/api/utils/mime"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	"ocm.software/ocm/api/ocm/extensions/artifacttypes"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/utils/accessio"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"ocm.software/ocm/api/utils/accessobj"
	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	environment "ocm.software/ocm/api/helper/env"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/compression"
	ocmPkg "github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/test"
)

var ()

const (
	CTFPath          = "ocm-k8s-ctfstore--*"
	RepositoryObj    = "test-repository"
	Component        = "ocm.software/test-component"
	ComponentVersion = "1.0.0"
	ResourceObj      = "test-resource"
	ResourceVersion  = "1.0.0"
	ResourceContent  = "some important content"
)

var _ = Describe("Resource Controller", func() {
	var (
		env               *Builder
		resourceLocalPath string
	)

	Context("resource controller", func() {
		var componentName string
		var componentObj *v1alpha1.Component
		var namespace *corev1.Namespace

		BeforeEach(func(ctx SpecContext) {
			namespaceName := test.GenerateNamespace(ctx.SpecReport().LeafNodeText)
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

			componentName = test.GenerateComponentName(ctx.SpecReport().LeafNodeText)

			resourceLocalPath = Must(os.MkdirTemp("", CTFPath))
			DeferCleanup(func() error {
				return os.RemoveAll(resourceLocalPath)
			})

			env = NewBuilder(environment.FileSystem(osfs.OsFs))
			DeferCleanup(env.Cleanup)
		})

		AfterEach(func(ctx SpecContext) {
			By("deleting the component")
			Expect(k8sClient.Delete(ctx, componentObj)).To(Succeed())
			Eventually(func(ctx context.Context) bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(componentObj), componentObj)
				return errors.IsNotFound(err)
			}).WithContext(ctx).Should(BeTrue())

			resources := &v1alpha1.ResourceList{}
			Expect(k8sClient.List(ctx, resources, client.InNamespace(namespace.GetName()))).To(Succeed())
			Expect(resources.Items).To(HaveLen(0))

			// TODO: test if OCI artifact was deleted
		})

		Context("resource controller", func() {
			It("can reconcile a plaintext resource", func() {
				resourceType := artifacttypes.PLAIN_TEXT

				By("creating an ocm resource from a plain text")
				env.OCMCommonTransport(resourceLocalPath, accessio.FormatDirectory, func() {
					env.Component(Component, func() {
						env.Version(ComponentVersion, func() {
							env.Resource(ResourceObj, ResourceVersion, resourceType, v1.LocalRelation, func() {
								env.BlobData(mime.MIME_TEXT, []byte(ResourceContent))
							})
						})
					})
				})

				repo, err := ctf.Open(env, accessobj.ACC_WRITABLE, resourceLocalPath, vfs.FileMode(vfs.O_RDWR), env)
				Expect(err).NotTo(HaveOccurred())
				cv, err := repo.LookupComponentVersion(Component, ComponentVersion)
				Expect(err).NotTo(HaveOccurred())
				cd, err := ocmPkg.ListComponentDescriptors(ctx, cv, repo)
				Expect(err).NotTo(HaveOccurred())
				dataCds, err := yaml.Marshal(cd)
				Expect(err).NotTo(HaveOccurred())

				spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, resourceLocalPath)
				specData, err := spec.MarshalJSON()

				By("creating a mocked component")
				componentObj = test.SetupComponentWithDescriptorList(ctx, componentName, namespace.GetName(), dataCds, &test.MockComponentOptions{
					Registry: registry,
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      Component,
						Version:        ComponentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
					},
					Repository: RepositoryObj,
				})

				By("creating a resource object")
				resource := &v1alpha1.Resource{
					ObjectMeta: k8smetav1.ObjectMeta{
						Namespace: namespace.GetName(),
						Name:      ResourceObj,
					},
					Spec: v1alpha1.ResourceSpec{
						ComponentRef: corev1.LocalObjectReference{
							Name: componentName,
						},
						Resource: v1alpha1.ResourceID{
							ByReference: v1alpha1.ResourceReference{
								Resource: v1.NewIdentity(ResourceObj),
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				By("checking that the resource has been reconciled successfully")
				Eventually(func(ctx context.Context) bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)
					if err != nil {
						return false
					}
					return conditions.IsReady(resource)
				}, "15s").WithContext(ctx).Should(BeTrue())
				Expect(resource).To(HaveField("Status.Resource.Name", Equal(ResourceObj)))
				Expect(resource).To(HaveField("Status.Resource.Type", Equal(resourceType)))
				Expect(resource).To(HaveField("Status.Resource.Version", Equal(ResourceVersion)))

				resourceAcc, err := cv.GetResource(v1.NewIdentity(ResourceObj))
				Expect(err).NotTo(HaveOccurred())
				validateArtifact(ctx, resource, resourceAcc)

				By("delete resource manually")
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				Eventually(func(ctx context.Context) bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)
					return errors.IsNotFound(err)
				}, "15s").WithContext(ctx).Should(BeTrue())
			})

			It("can reconcile a compressed plaintext resource", func() {
				resourceType := artifacttypes.PLAIN_TEXT

				// The resource controller will only gzip-compress the content, if it is not already compressed. Thus, we
				// expect the content to be only compressed once.
				resourceContentCompressed, err := compression.AutoCompressAsGzip(ctx, []byte(ResourceContent))
				Expect(err).NotTo(HaveOccurred())

				By("creating an ocm resource from a plain text")
				env.OCMCommonTransport(resourceLocalPath, accessio.FormatDirectory, func() {
					env.Component(Component, func() {
						env.Version(ComponentVersion, func() {
							env.Resource(ResourceObj, ResourceVersion, resourceType, v1.LocalRelation, func() {
								env.BlobData(mime.MIME_TEXT, resourceContentCompressed)
							})
						})
					})
				})

				repo, err := ctf.Open(env, accessobj.ACC_WRITABLE, resourceLocalPath, vfs.FileMode(vfs.O_RDWR), env)
				Expect(err).NotTo(HaveOccurred())
				cv, err := repo.LookupComponentVersion(Component, ComponentVersion)
				Expect(err).NotTo(HaveOccurred())
				cd, err := ocmPkg.ListComponentDescriptors(ctx, cv, repo)
				Expect(err).NotTo(HaveOccurred())
				dataCds, err := yaml.Marshal(cd)
				Expect(err).NotTo(HaveOccurred())

				spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, resourceLocalPath)
				specData, err := spec.MarshalJSON()

				By("creating a mocked component")
				componentObj = test.SetupComponentWithDescriptorList(ctx, componentName, namespace.GetName(), dataCds, &test.MockComponentOptions{
					Registry: registry,
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      Component,
						Version:        ComponentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
					},
					Repository: RepositoryObj,
				})

				By("creating a resource object")
				resource := &v1alpha1.Resource{
					ObjectMeta: k8smetav1.ObjectMeta{
						Namespace: namespace.GetName(),
						Name:      ResourceObj,
					},
					Spec: v1alpha1.ResourceSpec{
						ComponentRef: corev1.LocalObjectReference{
							Name: componentName,
						},
						Resource: v1alpha1.ResourceID{
							ByReference: v1alpha1.ResourceReference{
								Resource: v1.NewIdentity(ResourceObj),
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				By("checking that the resource has been reconciled successfully")
				Eventually(func(ctx context.Context) bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)
					if err != nil {
						return false
					}
					return conditions.IsReady(resource)
				}, "15s").WithContext(ctx).Should(BeTrue())
				Expect(resource).To(HaveField("Status.Resource.Name", Equal(ResourceObj)))
				Expect(resource).To(HaveField("Status.Resource.Type", Equal(resourceType)))
				Expect(resource).To(HaveField("Status.Resource.Version", Equal(ResourceVersion)))

				resourceAcc, err := cv.GetResource(v1.NewIdentity(ResourceObj))
				Expect(err).NotTo(HaveOccurred())
				validateArtifact(ctx, resource, resourceAcc)

				By("delete resource manually")
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				Eventually(func(ctx context.Context) bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)
					return errors.IsNotFound(err)
				}, "15s").WithContext(ctx).Should(BeTrue())
			})

			It("can reconcile a OCI artifact resource", func() {
				resourceType := artifacttypes.OCI_ARTIFACT

				By("creating an OCI artifact")
				repository, err := registry.NewRepository(ctx, ResourceObj)
				Expect(err).NotTo(HaveOccurred())
				contentCompressed, err := compression.AutoCompressAsGzip(ctx, []byte(ResourceContent))
				Expect(err).ToNot(HaveOccurred())
				manifestDigest, err := repository.PushArtifact(ctx, ResourceVersion, contentCompressed)
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func(ctx SpecContext) {
					Expect(repository.DeleteArtifact(ctx, manifestDigest.String())).To(Succeed())
				})

				By("creating an ocm resource from an OCI artifact")
				env.OCMCommonTransport(resourceLocalPath, accessio.FormatDirectory, func() {
					env.Component(Component, func() {
						env.Version(ComponentVersion, func() {
							env.Resource(ResourceObj, ResourceVersion, resourceType, v1.LocalRelation, func() {
								env.Access(ociartifact.New(fmt.Sprintf("http://%s/%s:%s", repository.GetHost(), repository.GetName(), ResourceVersion)))
							})
						})
					})
				})

				repo, err := ctf.Open(env, accessobj.ACC_WRITABLE, resourceLocalPath, vfs.FileMode(vfs.O_RDWR), env)
				Expect(err).NotTo(HaveOccurred())
				cv, err := repo.LookupComponentVersion(Component, ComponentVersion)
				Expect(err).NotTo(HaveOccurred())
				cd, err := ocmPkg.ListComponentDescriptors(ctx, cv, repo)
				Expect(err).NotTo(HaveOccurred())
				dataCds, err := yaml.Marshal(cd)
				Expect(err).NotTo(HaveOccurred())

				spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, resourceLocalPath)
				specData, err := spec.MarshalJSON()
				Expect(err).NotTo(HaveOccurred())

				By("creating a mocked component")
				componentObj = test.SetupComponentWithDescriptorList(ctx, componentName, namespace.GetName(), dataCds, &test.MockComponentOptions{
					Registry: registry,
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      Component,
						Version:        ComponentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
					},
					Repository: RepositoryObj,
				})

				By("creating a resource object")
				resource := &v1alpha1.Resource{
					ObjectMeta: k8smetav1.ObjectMeta{
						Namespace: namespace.GetName(),
						Name:      ResourceObj,
					},
					Spec: v1alpha1.ResourceSpec{
						ComponentRef: corev1.LocalObjectReference{
							Name: componentName,
						},
						Resource: v1alpha1.ResourceID{
							ByReference: v1alpha1.ResourceReference{
								Resource: v1.NewIdentity(ResourceObj),
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				By("checking that the resource has been reconciled successfully")
				Eventually(func(ctx context.Context) bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)
					if err != nil {
						return false
					}
					return conditions.IsReady(resource)
				}, "15s").WithContext(ctx).Should(BeTrue())
				Expect(resource).To(HaveField("Status.Resource.Name", Equal(ResourceObj)))
				Expect(resource).To(HaveField("Status.Resource.Type", Equal(resourceType)))
				Expect(resource).To(HaveField("Status.Resource.Version", Equal(ResourceVersion)))

				resourceAcc, err := cv.GetResource(v1.NewIdentity(ResourceObj))
				Expect(err).NotTo(HaveOccurred())
				validateArtifact(ctx, resource, resourceAcc)

				By("delete resource manually")
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				Eventually(func(ctx context.Context) bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)
					return errors.IsNotFound(err)
				}, "15s").WithContext(ctx).Should(BeTrue())
			})

			// TODO: Add more testcases
		})
	})
})

func validateArtifact(ctx context.Context, resource *v1alpha1.Resource, resourceAccess ocm.ResourceAccess) {
	GinkgoHelper()

	By("checking that resource has a reference to OCI artifact")
	Eventually(komega.Object(resource), "15s").Should(
		HaveField("Status.OCIArtifact", Not(BeNil())))

	By("checking that the OCI artifact contains the correct content")
	ociRepo := Must(registry.NewRepository(ctx, resource.GetOCIRepository()))
	contentCompressed := Must(ociRepo.FetchArtifact(ctx, resource.GetManifestDigest()))
	gzipReader, err := gzip.NewReader(bytes.NewReader(contentCompressed))
	Expect(err).NotTo(HaveOccurred())
	content, err := io.ReadAll(gzipReader)
	Expect(string(content)).To(Equal(ResourceContent))

	Expect(resource.GetBlobDigest()).To(Equal(resourceAccess.Meta().Digest.Value))
	Expect(resource.Status.OCIArtifact.Blob.Tag).To(Equal(ResourceVersion))
	if resourceAccess.Meta().GetType() == "ociArtifact" {
		// OCI artifacts are only copied and no information about the blob length is available (open TODO).
		Expect(resource.Status.OCIArtifact.Blob.Size).To(Equal(int64(0)))
	} else {
		Expect(resource.Status.OCIArtifact.Blob.Size).To(Equal(int64(len(contentCompressed))))
	}
}

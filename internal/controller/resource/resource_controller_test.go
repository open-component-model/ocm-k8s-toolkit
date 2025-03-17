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
	"github.com/opencontainers/go-digest"
	"k8s.io/apimachinery/pkg/api/errors"
	. "ocm.software/ocm/api/helper/builder"
	"ocm.software/ocm/api/ocm"
	ocmociartifact "ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
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
	toolkitociartifact "github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
	ocmPkg "github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/test"
)

const (
	CTFPath          = "ocm-k8s-ctfstore--*"
	RepositoryObj    = "test-repository"
	ComponentObj     = "test-component"
	ResourceObj      = "test-resource"
	ComponentVersion = "1.0.0"
	ResourceVersion  = "1.0.0"
	ResourceContent  = "some important content"
)

var _ = Describe("Resource Controller", func() {
	var (
		env               *Builder
		resourceLocalPath string
	)

	Context("resource controller", func() {
		var componentName, resourceName string
		var componentObj *v1alpha1.Component
		var namespace *corev1.Namespace

		BeforeEach(func(ctx SpecContext) {
			namespaceName := test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

			componentName = "ocm.software/component-" + test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			resourceName = "resource-" + test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)

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
		})

		Context("resource controller", func() {
			It("can reconcile a plaintext resource", func() {
				resourceType := artifacttypes.PLAIN_TEXT

				By("creating an ocm resource from a plain text")
				env.OCMCommonTransport(resourceLocalPath, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(ComponentVersion, func() {
							env.Resource(resourceName, ResourceVersion, resourceType, v1.LocalRelation, func() {
								env.BlobData(mime.MIME_TEXT, []byte(ResourceContent))
							})
						})
					})
				})

				repo, err := ctf.Open(env, accessobj.ACC_WRITABLE, resourceLocalPath, vfs.FileMode(vfs.O_RDWR), env)
				Expect(err).NotTo(HaveOccurred())
				cv, err := repo.LookupComponentVersion(componentName, ComponentVersion)
				Expect(err).NotTo(HaveOccurred())
				cd, err := ocmPkg.ListComponentDescriptors(ctx, cv, repo)
				Expect(err).NotTo(HaveOccurred())
				dataCds, err := yaml.Marshal(cd)
				Expect(err).NotTo(HaveOccurred())

				spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, resourceLocalPath)
				specData, err := spec.MarshalJSON()

				By("creating a mocked component")
				componentObj = test.SetupComponentWithDescriptorList(ctx, ComponentObj, namespace.GetName(), dataCds, &test.MockComponentOptions{
					Registry: registry,
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      componentName,
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
							Name: ComponentObj,
						},
						Resource: v1alpha1.ResourceID{
							ByReference: v1alpha1.ResourceReference{
								Resource: v1.NewIdentity(resourceName),
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				By("checking that the resource has been reconciled successfully")
				waitUntilResourceIsReady(ctx, resource)
				Expect(resource).To(HaveField("Status.Resource.Name", Equal(resourceName)))
				Expect(resource).To(HaveField("Status.Resource.Type", Equal(resourceType)))
				Expect(resource).To(HaveField("Status.Resource.Version", Equal(ResourceVersion)))

				resourceAcc, err := cv.GetResource(v1.NewIdentity(resourceName))
				Expect(err).NotTo(HaveOccurred())
				validateArtifact(ctx, resource, resourceAcc, ResourceVersion, ResourceContent)

				By("delete resource manually")
				deleteResource(ctx, resource)
			})

			It("can reconcile a compressed plaintext resource", func() {
				resourceType := artifacttypes.PLAIN_TEXT

				// The resource controller will only gzip-compress the content, if it is not already compressed. Thus, we
				// expect the content to be only compressed once.
				resourceContentCompressed, err := compression.AutoCompressAsGzip(ctx, []byte(ResourceContent))
				Expect(err).NotTo(HaveOccurred())

				By("creating an ocm resource from a plain text")
				env.OCMCommonTransport(resourceLocalPath, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(ComponentVersion, func() {
							env.Resource(resourceName, ResourceVersion, resourceType, v1.LocalRelation, func() {
								env.BlobData(mime.MIME_TEXT, resourceContentCompressed)
							})
						})
					})
				})

				repo, err := ctf.Open(env, accessobj.ACC_WRITABLE, resourceLocalPath, vfs.FileMode(vfs.O_RDWR), env)
				Expect(err).NotTo(HaveOccurred())
				cv, err := repo.LookupComponentVersion(componentName, ComponentVersion)
				Expect(err).NotTo(HaveOccurred())
				cd, err := ocmPkg.ListComponentDescriptors(ctx, cv, repo)
				Expect(err).NotTo(HaveOccurred())
				dataCds, err := yaml.Marshal(cd)
				Expect(err).NotTo(HaveOccurred())

				spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, resourceLocalPath)
				specData, err := spec.MarshalJSON()

				By("creating a mocked component")
				componentObj = test.SetupComponentWithDescriptorList(ctx, ComponentObj, namespace.GetName(), dataCds, &test.MockComponentOptions{
					Registry: registry,
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      componentName,
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
							Name: ComponentObj,
						},
						Resource: v1alpha1.ResourceID{
							ByReference: v1alpha1.ResourceReference{
								Resource: v1.NewIdentity(resourceName),
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				By("checking that the resource has been reconciled successfully")
				waitUntilResourceIsReady(ctx, resource)
				Expect(resource).To(HaveField("Status.Resource.Name", Equal(resourceName)))
				Expect(resource).To(HaveField("Status.Resource.Type", Equal(resourceType)))
				Expect(resource).To(HaveField("Status.Resource.Version", Equal(ResourceVersion)))

				resourceAcc, err := cv.GetResource(v1.NewIdentity(resourceName))
				Expect(err).NotTo(HaveOccurred())
				validateArtifact(ctx, resource, resourceAcc, ResourceVersion, ResourceContent)

				By("delete resource manually")
				deleteResource(ctx, resource)
			})

			It("can reconcile a OCI artifact resource", func() {
				resourceType := artifacttypes.OCI_ARTIFACT

				By("creating an OCI artifact")
				repository, err := registry.NewRepository(ctx, resourceName)
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
					env.Component(componentName, func() {
						env.Version(ComponentVersion, func() {
							env.Resource(resourceName, ResourceVersion, resourceType, v1.LocalRelation, func() {
								env.Access(ocmociartifact.New(fmt.Sprintf("http://%s/%s:%s", repository.GetHost(), repository.GetName(), ResourceVersion)))
							})
						})
					})
				})

				repo, err := ctf.Open(env, accessobj.ACC_WRITABLE, resourceLocalPath, vfs.FileMode(vfs.O_RDWR), env)
				Expect(err).NotTo(HaveOccurred())
				cv, err := repo.LookupComponentVersion(componentName, ComponentVersion)
				Expect(err).NotTo(HaveOccurred())
				cd, err := ocmPkg.ListComponentDescriptors(ctx, cv, repo)
				Expect(err).NotTo(HaveOccurred())
				dataCds, err := yaml.Marshal(cd)
				Expect(err).NotTo(HaveOccurred())

				spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, resourceLocalPath)
				specData, err := spec.MarshalJSON()
				Expect(err).NotTo(HaveOccurred())

				By("creating a mocked component")
				componentObj = test.SetupComponentWithDescriptorList(ctx, ComponentObj, namespace.GetName(), dataCds, &test.MockComponentOptions{
					Registry: registry,
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      componentName,
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
							Name: ComponentObj,
						},
						Resource: v1alpha1.ResourceID{
							ByReference: v1alpha1.ResourceReference{
								Resource: v1.NewIdentity(resourceName),
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				By("checking that the resource has been reconciled successfully")
				waitUntilResourceIsReady(ctx, resource)
				Expect(resource).To(HaveField("Status.Resource.Name", Equal(resourceName)))
				Expect(resource).To(HaveField("Status.Resource.Type", Equal(resourceType)))
				Expect(resource).To(HaveField("Status.Resource.Version", Equal(ResourceVersion)))

				resourceAcc, err := cv.GetResource(v1.NewIdentity(resourceName))
				Expect(err).NotTo(HaveOccurred())
				validateArtifact(ctx, resource, resourceAcc, ResourceVersion, ResourceContent)

				By("delete resource manually")
				deleteResource(ctx, resource)
			})

			It("can reconcile a plaintext resource with a plus in the version", func() {
				resourceType := artifacttypes.PLAIN_TEXT
				resourceVersionPlus := ResourceVersion + "+resourceVersionSuffix"
				expectedBlobTag := "1.0.0.build-resourceVersionSuffix"

				By("creating an ocm resource from a plain text")
				env.OCMCommonTransport(resourceLocalPath, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(ComponentVersion, func() {
							env.Resource(resourceName, resourceVersionPlus, resourceType, v1.LocalRelation, func() {
								env.BlobData(mime.MIME_TEXT, []byte(ResourceContent))
							})
						})
					})
				})

				repo, err := ctf.Open(env, accessobj.ACC_WRITABLE, resourceLocalPath, vfs.FileMode(vfs.O_RDWR), env)
				Expect(err).NotTo(HaveOccurred())
				cv, err := repo.LookupComponentVersion(componentName, ComponentVersion)
				Expect(err).NotTo(HaveOccurred())
				cd, err := ocmPkg.ListComponentDescriptors(ctx, cv, repo)
				Expect(err).NotTo(HaveOccurred())
				dataCds, err := yaml.Marshal(cd)
				Expect(err).NotTo(HaveOccurred())

				spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, resourceLocalPath)
				specData, err := spec.MarshalJSON()

				By("creating a mocked component")
				componentObj = test.SetupComponentWithDescriptorList(ctx, ComponentObj, namespace.GetName(), dataCds, &test.MockComponentOptions{
					Registry: registry,
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      componentName,
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
							Name: ComponentObj,
						},
						Resource: v1alpha1.ResourceID{
							ByReference: v1alpha1.ResourceReference{
								Resource: v1.NewIdentity(resourceName),
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				By("checking that the resource has been reconciled successfully")
				waitUntilResourceIsReady(ctx, resource)
				Expect(resource).To(HaveField("Status.Resource.Name", Equal(resourceName)))
				Expect(resource).To(HaveField("Status.Resource.Type", Equal(resourceType)))
				Expect(resource).To(HaveField("Status.Resource.Version", Equal(resourceVersionPlus)))

				resourceAcc, err := cv.GetResource(v1.NewIdentity(resourceName))
				Expect(err).NotTo(HaveOccurred())
				validateArtifact(ctx, resource, resourceAcc, expectedBlobTag, ResourceContent)

				By("delete resource manually")
				deleteResource(ctx, resource)
			})

			It("can reconcile when component changes", func() {
				resourceType := artifacttypes.PLAIN_TEXT

				By("creating an ocm resource from a plain text")
				env.OCMCommonTransport(resourceLocalPath, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(ComponentVersion, func() {
							env.Resource(resourceName, ResourceVersion, resourceType, v1.LocalRelation, func() {
								env.BlobData(mime.MIME_TEXT, []byte(ResourceContent))
							})
						})
					})
				})

				repo, err := ctf.Open(env, accessobj.ACC_WRITABLE, resourceLocalPath, vfs.FileMode(vfs.O_RDWR), env)
				Expect(err).NotTo(HaveOccurred())
				cv, err := repo.LookupComponentVersion(componentName, ComponentVersion)
				Expect(err).NotTo(HaveOccurred())
				cd, err := ocmPkg.ListComponentDescriptors(ctx, cv, repo)
				Expect(err).NotTo(HaveOccurred())
				dataCds, err := yaml.Marshal(cd)
				Expect(err).NotTo(HaveOccurred())

				spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, resourceLocalPath)
				specData, err := spec.MarshalJSON()

				By("creating a mocked component")
				componentObj = test.SetupComponentWithDescriptorList(ctx, ComponentObj, namespace.GetName(), dataCds, &test.MockComponentOptions{
					Registry: registry,
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      componentName,
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
							Name: ComponentObj,
						},
						Resource: v1alpha1.ResourceID{
							ByReference: v1alpha1.ResourceReference{
								Resource: v1.NewIdentity(resourceName),
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				By("checking that the resource has been reconciled successfully")
				waitUntilResourceIsReady(ctx, resource)
				Expect(resource).To(HaveField("Status.Resource.Name", Equal(resourceName)))
				Expect(resource).To(HaveField("Status.Resource.Type", Equal(resourceType)))
				Expect(resource).To(HaveField("Status.Resource.Version", Equal(ResourceVersion)))

				resourceAcc, err := cv.GetResource(v1.NewIdentity(resourceName))
				Expect(err).NotTo(HaveOccurred())
				validateArtifact(ctx, resource, resourceAcc, ResourceVersion, ResourceContent)

				// Save artifact information to check afterward, that it has been deleted as obsolete.
				artifactBeforeUpdate := resource.GetOCIArtifact().DeepCopy()

				// Increase component and resource versions
				By("creating new component version in the file system")
				const ComponentVersionNew = "1.0.1-component"
				const ResourceVersionNew = "1.0.1-resource"
				const ResourceContentNew = "another important content"
				env.OCMCommonTransport(resourceLocalPath, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(ComponentVersion, func() {
							env.Resource(resourceName, ResourceVersion, resourceType, v1.LocalRelation, func() {
								env.BlobData(mime.MIME_TEXT, []byte(ResourceContent))
							})
						})
					})
					env.Component(componentName, func() {
						env.Version(ComponentVersionNew, func() {
							env.Resource(resourceName, ResourceVersionNew, resourceType, v1.LocalRelation, func() {
								env.BlobData(mime.MIME_TEXT, []byte(ResourceContentNew))
							})
						})
					})
				})

				By("getting new component descriptors")
				repo, err = ctf.Open(env, accessobj.ACC_WRITABLE, resourceLocalPath, vfs.FileMode(vfs.O_RDWR), env)
				Expect(err).NotTo(HaveOccurred())
				cv, err = repo.LookupComponentVersion(componentName, ComponentVersionNew)
				Expect(err).NotTo(HaveOccurred())
				cd, err = ocmPkg.ListComponentDescriptors(ctx, cv, repo)
				Expect(err).NotTo(HaveOccurred())
				dataCds, err = yaml.Marshal(cd)
				Expect(err).NotTo(HaveOccurred())

				By("uploading new component descriptors as OCI artifact")
				repositoryName, err := toolkitociartifact.CreateRepositoryName(RepositoryObj, componentName)
				Expect(err).ToNot(HaveOccurred())
				repository, err := registry.NewRepository(ctx, repositoryName)
				Expect(err).ToNot(HaveOccurred())
				manifestDigest, err := repository.PushArtifact(ctx, ComponentVersionNew, dataCds)
				Expect(err).ToNot(HaveOccurred())

				By("updating mock component status")
				// Get fresh component resource.
				componentObj = &v1alpha1.Component{
					ObjectMeta: k8smetav1.ObjectMeta{
						Namespace: componentObj.GetNamespace(),
						Name:      componentObj.GetName(),
					},
				}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(componentObj), componentObj)).To(Succeed())

				// Update status fields.
				componentObj.Status.OCIArtifact = &v1alpha1.OCIArtifactInfo{
					Repository: repositoryName,
					Digest:     manifestDigest.String(),
					Blob: &v1alpha1.BlobInfo{
						Digest: digest.FromBytes(dataCds).String(),
						Tag:    ComponentVersionNew,
						Size:   int64(len(dataCds)),
					},
				}
				componentObj.Status.Component.Component = componentName
				componentObj.Status.Component.Version = ComponentVersionNew
				componentObj.Status.Component.RepositorySpec = &apiextensionsv1.JSON{Raw: specData}
				Expect(k8sClient.Status().Update(ctx, componentObj)).To(Succeed())

				By("updating mock component spec")
				// Get fresh component resource.
				componentObj = &v1alpha1.Component{
					ObjectMeta: k8smetav1.ObjectMeta{
						Namespace: componentObj.GetNamespace(),
						Name:      componentObj.GetName(),
					},
				}
				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(componentObj), componentObj)
				Expect(err).ToNot(HaveOccurred())

				// Update spec fields.
				// This step should trigger reconciliation of the dependent resource object.
				componentObj.Spec.Semver = ComponentVersionNew
				Expect(k8sClient.Update(ctx, componentObj)).To(Succeed())

				By("checking that the resource now refers to new OCI artifact")
				resource = &v1alpha1.Resource{
					ObjectMeta: k8smetav1.ObjectMeta{
						Namespace: resource.GetNamespace(),
						Name:      resource.GetName(),
					},
				}
				Eventually(func(g Gomega, ctx context.Context) bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)
					if err != nil {
						return false
					}
					g.Expect(resource.Status.OCIArtifact.Blob.Tag).To(Equal(ResourceVersionNew))

					return conditions.IsReady(resource)
				}, "15s").WithContext(ctx).Should(BeTrue())

				resourceAcc, err = cv.GetResource(v1.NewIdentity(resourceName))
				Expect(err).NotTo(HaveOccurred())
				validateArtifact(ctx, resource, resourceAcc, ResourceVersionNew, ResourceContentNew)

				By("checking if the previous artifact was deleted")
				test.ExpectArtifactToNotExist(ctx, registry, artifactBeforeUpdate)

				By("delete resource manually")
				deleteResource(ctx, resource)
			})

			It("can reconcile when resource changes", func() {
				resourceType := artifacttypes.PLAIN_TEXT
				resourceNameB := test.SanitizeNameForK8s("b-" + resourceName)
				resourceVersionB := ResourceVersion + "-resource-b"
				resourceContentB := ResourceContent + " - Resource B"

				By("creating an ocm resource from a plain text")
				env.OCMCommonTransport(resourceLocalPath, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(ComponentVersion, func() {
							env.Resource(resourceName, ResourceVersion, resourceType, v1.LocalRelation, func() {
								env.BlobData(mime.MIME_TEXT, []byte(ResourceContent))
							})
							env.Resource(resourceNameB, resourceVersionB, resourceType, v1.LocalRelation, func() {
								env.BlobData(mime.MIME_TEXT, []byte(resourceContentB))
							})
						})
					})
				})

				repo, err := ctf.Open(env, accessobj.ACC_WRITABLE, resourceLocalPath, vfs.FileMode(vfs.O_RDWR), env)
				Expect(err).NotTo(HaveOccurred())
				cv, err := repo.LookupComponentVersion(componentName, ComponentVersion)
				Expect(err).NotTo(HaveOccurred())
				cd, err := ocmPkg.ListComponentDescriptors(ctx, cv, repo)
				Expect(err).NotTo(HaveOccurred())
				dataCds, err := yaml.Marshal(cd)
				Expect(err).NotTo(HaveOccurred())

				spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, resourceLocalPath)
				specData, err := spec.MarshalJSON()

				By("creating a mocked component")
				componentObj = test.SetupComponentWithDescriptorList(ctx, ComponentObj, namespace.GetName(), dataCds, &test.MockComponentOptions{
					Registry: registry,
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      componentName,
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
							Name: ComponentObj,
						},
						Resource: v1alpha1.ResourceID{
							ByReference: v1alpha1.ResourceReference{
								Resource: v1.NewIdentity(resourceName),
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				By("checking that the resource has been reconciled successfully")
				waitUntilResourceIsReady(ctx, resource)
				Expect(resource).To(HaveField("Status.Resource.Name", Equal(resourceName)))
				Expect(resource).To(HaveField("Status.Resource.Type", Equal(resourceType)))
				Expect(resource).To(HaveField("Status.Resource.Version", Equal(ResourceVersion)))

				resourceAcc, err := cv.GetResource(v1.NewIdentity(resourceName))
				Expect(err).NotTo(HaveOccurred())
				validateArtifact(ctx, resource, resourceAcc, ResourceVersion, ResourceContent)

				// Save artifact information to check afterward, that it has been deleted as obsolete.
				artifactBeforeUpdate := resource.GetOCIArtifact().DeepCopy()

				By("changing the resource")
				resource = &v1alpha1.Resource{
					ObjectMeta: k8smetav1.ObjectMeta{
						Namespace: resource.GetNamespace(),
						Name:      resource.GetName(),
					},
				}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)).To(Succeed())

				identityB := v1.NewIdentity(resourceNameB)
				resource.Spec.Resource.ByReference.Resource = identityB
				Expect(k8sClient.Update(ctx, resource)).To(Succeed())

				By("checking that the resource now refers to new OCI artifact")
				Eventually(func(g Gomega, ctx context.Context) bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)
					if err != nil {
						return false
					}
					g.Expect(resource.Status.OCIArtifact.Blob.Tag).To(Equal(resourceVersionB))

					return conditions.IsReady(resource)
				}, "15s").WithContext(ctx).Should(BeTrue())

				resourceAcc, err = cv.GetResource(identityB)
				Expect(err).NotTo(HaveOccurred())
				validateArtifact(ctx, resource, resourceAcc, resourceVersionB, resourceContentB)

				By("checking if the previous artifact was deleted")
				test.ExpectArtifactToNotExist(ctx, registry, artifactBeforeUpdate)

				By("delete resource manually")
				deleteResource(ctx, resource)
			})
		})
	})
})

func deleteResource(ctx context.Context, resource *v1alpha1.Resource) {
	GinkgoHelper()

	Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

	Eventually(func(ctx context.Context) bool {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)
		return errors.IsNotFound(err)
	}, "15s").WithContext(ctx).Should(BeTrue())

	test.ExpectArtifactToNotExist(ctx, registry, resource.GetOCIArtifact())
}

func validateArtifact(ctx context.Context, resource *v1alpha1.Resource, resourceAccess ocm.ResourceAccess, expectedTag, expectedContent string) {
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
	Expect(string(content)).To(Equal(expectedContent))

	Expect(resource.GetBlobDigest()).To(Equal(resourceAccess.Meta().Digest.Value))
	Expect(resource.Status.OCIArtifact.Blob.Tag).To(Equal(expectedTag))
	if resourceAccess.Meta().GetType() == "ociArtifact" {
		// OCI artifacts are only copied and no information about the blob length is available (open TODO).
		Expect(resource.Status.OCIArtifact.Blob.Size).To(Equal(int64(0)))
	} else {
		Expect(resource.Status.OCIArtifact.Blob.Size).To(Equal(int64(len(contentCompressed))))
	}
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
	}, "5m").WithContext(ctx).Should(Succeed())
}

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
	_ "embed"
	"fmt"
	"io"
	"os"

	. "github.com/mandelsoft/goutils/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "ocm.software/ocm/api/helper/builder"
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
	environment "ocm.software/ocm/api/helper/env"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	ocmPkg "github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/test"
)

var ()

const (
	CTFPath          = "ocm-k8s-ctfstore--*"
	Namespace        = "test-namespace"
	RepositoryObj    = "test-repository"
	Component        = "ocm.software/test-component"
	ComponentObj     = "test-component"
	ComponentVersion = "1.0.0"
	ResourceObj      = "test-resource"
	ResourceVersion  = "1.0.0"
	ResourceContent  = "some important content"
)

var _ = Describe("Resource Controller", func() {
	var (
		env               *Builder
		resourceLocalPath string
		testNumber        int
	)

	BeforeEach(func() {
		resourceLocalPath = Must(os.MkdirTemp("", CTFPath))
		DeferCleanup(func() error {
			return os.RemoveAll(resourceLocalPath)
		})

		env = NewBuilder(environment.FileSystem(osfs.OsFs))
		DeferCleanup(env.Cleanup)
		testNumber++
	})

	Context("resource controller", func() {
		It("can reconcile a resource: PlainText", func() {
			testComponent := fmt.Sprintf("%s-%d", ComponentObj, testNumber)
			testResource := fmt.Sprintf("%s-%d", ResourceObj, testNumber)
			resourceType := artifacttypes.PLAIN_TEXT

			By("creating an ocm resource from a plain text")
			env.OCMCommonTransport(resourceLocalPath, accessio.FormatDirectory, func() {
				env.Component(Component, func() {
					env.Version(ComponentVersion, func() {
						env.Resource(testResource, ResourceVersion, resourceType, v1.LocalRelation, func() {
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
			component := test.SetupComponentWithDescriptorList(ctx, testComponent, Namespace, dataCds, &test.MockComponentOptions{
				BasePath: "",
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
			DeferCleanup(func(ctx SpecContext) {
				Expect(k8sClient.Delete(ctx, component, client.PropagationPolicy(k8smetav1.DeletePropagationForeground))).To(Succeed())
			})

			By("creating a resource object")
			resource := &v1alpha1.Resource{
				ObjectMeta: k8smetav1.ObjectMeta{
					Namespace: Namespace,
					Name:      testResource,
				},
				Spec: v1alpha1.ResourceSpec{
					ComponentRef: corev1.LocalObjectReference{
						Name: testComponent,
					},
					Resource: v1alpha1.ResourceID{
						ByReference: v1alpha1.ResourceReference{
							Resource: v1.NewIdentity(testResource),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func(ctx SpecContext) {
				Expect(k8sClient.Delete(ctx, resource, client.PropagationPolicy(k8smetav1.DeletePropagationForeground))).To(Succeed())
			})

			By("checking that the resource has been reconciled successfully")
			Eventually(komega.Object(resource), "15s").Should(
				HaveField("Status.ObservedGeneration", Equal(int64(1))))

			Expect(resource).To(HaveField("Status.SnapshotRef.Name", Not(BeEmpty())))
			Expect(resource).To(HaveField("Status.Resource.Name", Equal(testResource)))
			Expect(resource).To(HaveField("Status.Resource.Type", Equal(resourceType)))
			Expect(resource).To(HaveField("Status.Resource.Version", Equal(ResourceVersion)))

			snapshotResource, err := snapshot.GetSnapshotForOwner(ctx, k8sClient, resource)
			Expect(err).NotTo(HaveOccurred())

			By("checking that the snapshot contains the correct content")
			snapshotRepository, err := registry.NewRepository(ctx, snapshotResource.Spec.Repository)
			Expect(err).NotTo(HaveOccurred())
			snapshotResourceContentReader, err := snapshotRepository.FetchSnapshot(ctx, snapshotResource.GetDigest())
			Expect(err).NotTo(HaveOccurred())
			snapshotResourceContent, err := io.ReadAll(snapshotResourceContentReader)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(snapshotResourceContent)).To(Equal(ResourceContent))

			// Compare other fields
			resourceAcc, err := cv.GetResource(v1.NewIdentity(testResource))
			Expect(err).NotTo(HaveOccurred())

			Expect(snapshotResource.Name).To(Equal(fmt.Sprintf("resource-%s", testResource)))
			Expect(snapshotResource.Spec.Blob.Digest).To(Equal(resourceAcc.Meta().Digest.Value))
			Expect(snapshotResource.Spec.Blob.Tag).To(Equal(ResourceVersion))
			Expect(snapshotResource.Spec.Blob.Size).To(Equal(int64(len([]byte(ResourceContent)))))
		})

		It("can reconcile a resource: OCIArtifact", func() {
			testComponent := fmt.Sprintf("%s-%d", ComponentObj, testNumber)
			testResource := fmt.Sprintf("%s-%d", ResourceObj, testNumber)
			resourceType := artifacttypes.OCI_ARTIFACT

			By("creating an OCI artifact")
			repository, err := registry.NewRepository(ctx, testResource)
			Expect(err).NotTo(HaveOccurred())
			manifestDigest, err := repository.PushSnapshot(ctx, ResourceVersion, []byte(ResourceContent))
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func(ctx SpecContext) {
				Expect(repository.DeleteSnapshot(ctx, manifestDigest.String())).To(Succeed())
			})

			By("creating an ocm resource from an OCI artifact")
			env.OCMCommonTransport(resourceLocalPath, accessio.FormatDirectory, func() {
				env.Component(Component, func() {
					env.Version(ComponentVersion, func() {
						env.Resource(testResource, ResourceVersion, resourceType, v1.LocalRelation, func() {
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
			component := test.SetupComponentWithDescriptorList(ctx, testComponent, Namespace, dataCds, &test.MockComponentOptions{
				BasePath: "",
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
			DeferCleanup(func(ctx SpecContext) {
				Expect(k8sClient.Delete(ctx, component, client.PropagationPolicy(k8smetav1.DeletePropagationForeground))).To(Succeed())
			})

			By("creating a resource object")
			resource := &v1alpha1.Resource{
				ObjectMeta: k8smetav1.ObjectMeta{
					Namespace: Namespace,
					Name:      testResource,
				},
				Spec: v1alpha1.ResourceSpec{
					ComponentRef: corev1.LocalObjectReference{
						Name: testComponent,
					},
					Resource: v1alpha1.ResourceID{
						ByReference: v1alpha1.ResourceReference{
							Resource: v1.NewIdentity(testResource),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func(ctx SpecContext) {
				Expect(k8sClient.Delete(ctx, resource, client.PropagationPolicy(k8smetav1.DeletePropagationForeground))).To(Succeed())
			})

			By("checking that the resource has been reconciled successfully")
			Eventually(komega.Object(resource), "5m").Should(
				HaveField("Status.ObservedGeneration", Equal(int64(1))))

			Expect(resource).To(HaveField("Status.SnapshotRef.Name", Not(BeEmpty())))
			Expect(resource).To(HaveField("Status.Resource.Name", Equal(testResource)))
			Expect(resource).To(HaveField("Status.Resource.Type", Equal(resourceType)))
			Expect(resource).To(HaveField("Status.Resource.Version", Equal(ResourceVersion)))

			snapshotResource, err := snapshot.GetSnapshotForOwner(ctx, k8sClient, resource)
			Expect(err).NotTo(HaveOccurred())

			By("checking that the snapshot contains the correct content")
			snapshotRepository, err := registry.NewRepository(ctx, snapshotResource.Spec.Repository)
			Expect(err).NotTo(HaveOccurred())
			snapshotResourceContentReader, err := snapshotRepository.FetchSnapshot(ctx, snapshotResource.GetDigest())
			Expect(err).NotTo(HaveOccurred())
			snapshotResourceContent, err := io.ReadAll(snapshotResourceContentReader)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(snapshotResourceContent)).To(Equal(ResourceContent))

			// Compare other fields
			resourceAcc, err := cv.GetResource(v1.NewIdentity(testResource))
			Expect(err).NotTo(HaveOccurred())

			Expect(snapshotResource.Name).To(Equal(fmt.Sprintf("resource-%s", testResource)))
			Expect(snapshotResource.Spec.Blob.Digest).To(Equal(resourceAcc.Meta().Digest.Value))
			Expect(snapshotResource.Spec.Blob.Tag).To(Equal(ResourceVersion))
			Expect(snapshotResource.Spec.Blob.Size).To(Equal(int64(0)))
		})

		// TODO: Add more testcases
	})
})

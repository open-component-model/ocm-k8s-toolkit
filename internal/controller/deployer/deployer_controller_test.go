package deployer

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"path/filepath"

	krov1alpha1 "github.com/kro-run/kro/api/v1alpha1"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/projectionfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	. "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"
	ocmmetav1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/extensions/artifacttypes"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/utils/accessio"
	"ocm.software/ocm/api/utils/mime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/status"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/test"
)

var _ = Describe("Deployer Controller", func() {
	var (
		env     *Builder
		tempDir string
	)

	rgd := []byte(`apiVersion: kro.run/v1alpha1
kind: ResourceGraphDefinition
metadata:
  name: valid-rgd
spec:
  schema:
    apiVersion: v1alpha1
    kind: SomeKind
    group: kro.run
    spec:
      testField: string
  resources:
    - id: exampleResource
      template:
        apiVersion: v1 
        kind: Pod
        metadata:
          name: some-name
        spec:
          container:
            - name: some-container
              image: some-image:latest`)

	rgdObj := &krov1alpha1.ResourceGraphDefinition{}
	Expect(yaml.Unmarshal(rgd, rgdObj)).To(Succeed())

	BeforeEach(func() {
		tempDir = GinkgoT().TempDir()
		fs, err := projectionfs.New(osfs.OsFs, tempDir)
		Expect(err).NotTo(HaveOccurred())
		env = NewBuilder(environment.FileSystem(fs))
	})
	AfterEach(func() {
		Expect(env.Cleanup()).To(Succeed())
	})

	Context("deployer controller", func() {
		var resourceObj *v1alpha1.Resource
		var namespace *corev1.Namespace
		var ctfName, componentName, resourceName, deployerObjName string
		var componentVersion string
		//repositoryName := "ocm.software/test-repository"

		BeforeEach(func(ctx SpecContext) {
			ctfName = "ctf-" + test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			componentName = "ocm.software/test-component-" + test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			resourceName = "test-resource-" + test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			deployerObjName = "test-deployer-" + test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
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
			By("deleting the resource")
			Expect(k8sClient.Delete(ctx, resourceObj)).To(Succeed())
			Eventually(func(ctx context.Context) error {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resourceObj), resourceObj)
				if err != nil {
					if errors.IsNotFound(err) {
						return nil
					}
					return err
				}

				return fmt.Errorf("resource %s still exists", resourceObj.Name)
			}).WithContext(ctx).Should(Succeed())

			deployers := &v1alpha1.DeployerList{}
			Expect(k8sClient.List(ctx, deployers)).To(Succeed())
			Expect(deployers.Items).To(HaveLen(0))

			RGDs := &krov1alpha1.ResourceGraphDefinitionList{}
			Expect(k8sClient.List(ctx, RGDs)).To(Succeed())
			Expect(RGDs.Items).To(HaveLen(0))
		})

		It("reconciles a deployer with a valid RGD", func(ctx SpecContext) {
			By("creating a CTF")
			resourceType := artifacttypes.PLAIN_TEXT
			resourceVersion := "1.0.0"
			env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version(componentVersion, func() {
						env.Resource(resourceName, resourceVersion, resourceType, ocmmetav1.LocalRelation, func() {
							env.BlobData(mime.MIME_TEXT, rgd)
						})
					})
				})
			})

			spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, filepath.Join(tempDir, ctfName))
			Expect(err).NotTo(HaveOccurred())
			specData, err := spec.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())

			By("mocking a resource")
			hashRgd := sha256.Sum256(rgd)
			resourceObj = test.MockResource(
				ctx,
				resourceName,
				namespace.GetName(),
				&test.MockResourceOptions{
					ComponentRef: corev1.LocalObjectReference{
						Name: componentName,
					},
					Clnt:     k8sClient,
					Recorder: recorder,
					ComponentInfo: &v1alpha1.ComponentInfo{
						Component:      componentName,
						Version:        componentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
					},
					ResourceInfo: &v1alpha1.ResourceInfo{
						Name:    resourceName,
						Type:    resourceType,
						Version: resourceVersion,
						Access:  apiextensionsv1.JSON{Raw: []byte("{}")},
						// TODO: Consider calculating the digest the ocm-way
						Digest: fmt.Sprintf("SHA-256:%s[%s]", hex.EncodeToString(hashRgd[:]), "genericBlobDigest/v1"),
					},
				},
			)

			By("creating a deployer")
			deployerObj := &v1alpha1.Deployer{
				ObjectMeta: metav1.ObjectMeta{
					Name: deployerObjName,
				},
				Spec: v1alpha1.DeployerSpec{
					ResourceRef: v1alpha1.ObjectKey{
						Name:      resourceObj.GetName(),
						Namespace: namespace.GetName(),
					},
				},
			}
			Expect(k8sClient.Create(ctx, deployerObj)).To(Succeed())

			By("checking that the deployer has been reconciled successfully")
			test.WaitForReadyObject(ctx, k8sClient, deployerObj, map[string]any{})

			By("checking that the deployed ResourceGraphDefinition is correct")
			rgdObjApplied := &krov1alpha1.ResourceGraphDefinition{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(rgdObj), rgdObjApplied)).To(Succeed())
			Expect(rgdObjApplied.Spec).To(Equal(rgdObj.Spec))

			By("mocking the GC")
			test.DeleteObject(ctx, k8sClient, rgdObj)

			By("deleting the deployer")
			test.DeleteObject(ctx, k8sClient, deployerObj)
		})

		It("does not reconcile a deployer with an invalid RGD", func(ctx SpecContext) {
			By("creating a CTF")
			resourceType := artifacttypes.PLAIN_TEXT
			resourceVersion := "1.0.0"
			invalidRgd := []byte("invalid-rgd")
			env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version(componentVersion, func() {
						env.Resource(resourceName, resourceVersion, resourceType, ocmmetav1.LocalRelation, func() {
							env.BlobData(mime.MIME_TEXT, invalidRgd)
						})
					})
				})
			})

			spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, filepath.Join(tempDir, ctfName))
			Expect(err).NotTo(HaveOccurred())
			specData, err := spec.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())

			By("mocking a resource")
			hashRgd := sha256.Sum256(invalidRgd)
			resourceObj = test.MockResource(
				ctx,
				resourceName,
				namespace.GetName(),
				&test.MockResourceOptions{
					ComponentRef: corev1.LocalObjectReference{
						Name: componentName,
					},
					Clnt:     k8sClient,
					Recorder: recorder,
					ComponentInfo: &v1alpha1.ComponentInfo{
						Component:      componentName,
						Version:        componentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
					},
					ResourceInfo: &v1alpha1.ResourceInfo{
						Name:    resourceName,
						Type:    resourceType,
						Version: resourceVersion,
						Access:  apiextensionsv1.JSON{Raw: []byte("{}")},
						// TODO: Consider calculating the digest the ocm-way
						Digest: fmt.Sprintf("SHA-256:%s[%s]", hex.EncodeToString(hashRgd[:]), "genericBlobDigest/v1"),
					},
				},
			)

			By("creating a deployer")
			deployerObj := &v1alpha1.Deployer{
				ObjectMeta: metav1.ObjectMeta{
					Name: deployerObjName,
				},
				Spec: v1alpha1.DeployerSpec{
					ResourceRef: v1alpha1.ObjectKey{
						Name:      resourceObj.GetName(),
						Namespace: namespace.GetName(),
					},
				},
			}
			Expect(k8sClient.Create(ctx, deployerObj)).To(Succeed())

			By("checking that the deployer has not been reconciled successfully")
			test.WaitForNotReadyObject(ctx, k8sClient, deployerObj, v1alpha1.MarshalFailedReason)

			By("deleting the resource")
			test.DeleteObject(ctx, k8sClient, deployerObj)
		})

		It("does not reconcile a deployer when the resource is not ready", func(ctx SpecContext) {
			By("mocking a resource")
			resourceObj = test.MockResource(
				ctx,
				resourceName,
				namespace.GetName(),
				&test.MockResourceOptions{
					ComponentRef: corev1.LocalObjectReference{
						Name: componentName,
					},
					Clnt:     k8sClient,
					Recorder: recorder,
					ComponentInfo: &v1alpha1.ComponentInfo{
						Component:      componentName,
						Version:        componentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: []byte("{}")},
					},
					ResourceInfo: &v1alpha1.ResourceInfo{
						Name:    resourceName,
						Type:    "resource-not-ready-type",
						Version: "v1.0.0",
						Access:  apiextensionsv1.JSON{Raw: []byte("{}")},
						// TODO: Consider calculating the digest the ocm-way
						Digest: "resource-not-ready-digest",
					},
				},
			)

			By("marking the mocked resource as not ready")
			resourceObjNotReady := &v1alpha1.Resource{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(resourceObj), resourceObjNotReady)).To(Succeed())
			status.MarkNotReady(recorder, resourceObjNotReady, v1alpha1.ResourceIsNotAvailable, "mock resource is not ready")
			Expect(k8sClient.Status().Update(ctx, resourceObjNotReady)).To(Succeed())

			By("creating a deployer")
			deployerObj := &v1alpha1.Deployer{
				ObjectMeta: metav1.ObjectMeta{
					Name: deployerObjName,
				},
				Spec: v1alpha1.DeployerSpec{
					ResourceRef: v1alpha1.ObjectKey{
						Name:      resourceObj.GetName(),
						Namespace: namespace.GetName(),
					},
				},
			}
			Expect(k8sClient.Create(ctx, deployerObj)).To(Succeed())

			By("checking that the deployer has not been reconciled successfully")
			test.WaitForNotReadyObject(ctx, k8sClient, deployerObj, v1alpha1.ResourceIsNotAvailable)

			By("deleting the resource")
			test.DeleteObject(ctx, k8sClient, deployerObj)
		})

		It("fails when the resource digest differs", func(ctx SpecContext) {
			By("creating a CTF")
			resourceType := artifacttypes.PLAIN_TEXT
			resourceVersion := "1.0.0"
			env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version(componentVersion, func() {
						env.Resource(resourceName, resourceVersion, resourceType, ocmmetav1.LocalRelation, func() {
							env.BlobData(mime.MIME_TEXT, rgd)
						})
					})
				})
			})

			spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, filepath.Join(tempDir, ctfName))
			Expect(err).NotTo(HaveOccurred())
			specData, err := spec.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())

			By("mocking a resource")
			resourceObj = test.MockResource(
				ctx,
				resourceName,
				namespace.GetName(),
				&test.MockResourceOptions{
					ComponentRef: corev1.LocalObjectReference{
						Name: componentName,
					},
					Clnt:     k8sClient,
					Recorder: recorder,
					ComponentInfo: &v1alpha1.ComponentInfo{
						Component:      componentName,
						Version:        componentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
					},
					ResourceInfo: &v1alpha1.ResourceInfo{
						Name:    resourceName,
						Type:    resourceType,
						Version: resourceVersion,
						Access:  apiextensionsv1.JSON{Raw: []byte("{}")},
						Digest:  "invalid-digest",
					},
				},
			)

			By("creating a deployer")
			deployerObj := &v1alpha1.Deployer{
				ObjectMeta: metav1.ObjectMeta{
					Name: deployerObjName,
				},
				Spec: v1alpha1.DeployerSpec{
					ResourceRef: v1alpha1.ObjectKey{
						Name:      resourceObj.GetName(),
						Namespace: namespace.GetName(),
					},
				},
			}
			Expect(k8sClient.Create(ctx, deployerObj)).To(Succeed())

			By("checking that the deployer has been reconciled successfully")
			test.WaitForNotReadyObject(ctx, k8sClient, deployerObj, v1alpha1.GetOCMResourceFailedReason)

			By("deleting the deployer")
			test.DeleteObject(ctx, k8sClient, deployerObj)
		})

		It("updates the RGD when the resource is updated with a valid change", func(ctx SpecContext) {
			By("creating a CTF")
			resourceType := artifacttypes.PLAIN_TEXT
			resourceVersion := "1.0.0"
			env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version(componentVersion, func() {
						env.Resource(resourceName, resourceVersion, resourceType, ocmmetav1.LocalRelation, func() {
							env.BlobData(mime.MIME_TEXT, rgd)
						})
					})
				})
			})

			spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, filepath.Join(tempDir, ctfName))
			Expect(err).NotTo(HaveOccurred())
			specData, err := spec.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())

			By("mocking a resource")
			hashRgd := sha256.Sum256(rgd)
			resourceObj = test.MockResource(
				ctx,
				resourceName,
				namespace.GetName(),
				&test.MockResourceOptions{
					ComponentRef: corev1.LocalObjectReference{
						Name: componentName,
					},
					Clnt:     k8sClient,
					Recorder: recorder,
					ComponentInfo: &v1alpha1.ComponentInfo{
						Component:      componentName,
						Version:        componentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
					},
					ResourceInfo: &v1alpha1.ResourceInfo{
						Name:    resourceName,
						Type:    resourceType,
						Version: resourceVersion,
						Access:  apiextensionsv1.JSON{Raw: []byte("{}")},
						// TODO: Consider calculating the digest the ocm-way
						Digest: fmt.Sprintf("SHA-256:%s[%s]", hex.EncodeToString(hashRgd[:]), "genericBlobDigest/v1"),
					},
				},
			)

			By("creating a deployer")
			deployerObj := &v1alpha1.Deployer{
				ObjectMeta: metav1.ObjectMeta{
					Name: deployerObjName,
				},
				Spec: v1alpha1.DeployerSpec{
					ResourceRef: v1alpha1.ObjectKey{
						Name:      resourceObj.GetName(),
						Namespace: namespace.GetName(),
					},
				},
			}
			Expect(k8sClient.Create(ctx, deployerObj)).To(Succeed())

			By("checking that the deployer has been reconciled successfully")
			test.WaitForReadyObject(ctx, k8sClient, deployerObj, map[string]any{})

			By("checking that the deployed ResourceGraphDefinition is correct")
			rgdObjApplied := &krov1alpha1.ResourceGraphDefinition{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(rgdObj), rgdObjApplied)).To(Succeed())
			Expect(rgdObjApplied.Spec).To(Equal(rgdObj.Spec))

			By("updating the mocked resource")
			componentVersion = "1.0.1"
			resourceVersion = "1.0.1"
			rgdObjApplied.Spec.Schema.Spec = runtime.RawExtension{Raw: []byte("{\"adjustedField\":\"string\"}")}
			rgdUpdated, err := yaml.Marshal(rgdObjApplied)
			Expect(err).NotTo(HaveOccurred())
			env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version(componentVersion, func() {
						env.Resource(resourceName, resourceVersion, resourceType, ocmmetav1.LocalRelation, func() {
							env.BlobData(mime.MIME_TEXT, rgdUpdated)
						})
					})
				})
			})

			spec, err = ctf.NewRepositorySpec(ctf.ACC_READONLY, filepath.Join(tempDir, ctfName))
			Expect(err).NotTo(HaveOccurred())
			specData, err = spec.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())

			By("updating the mocked resource")
			resourceObjNotReady := &v1alpha1.Resource{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(resourceObj), resourceObjNotReady)).To(Succeed())
			status.MarkNotReady(recorder, resourceObjNotReady, v1alpha1.ResourceIsNotAvailable, "mock resource is not ready")
			Expect(k8sClient.Status().Update(ctx, resourceObjNotReady)).To(Succeed())

			// All these field must be updated as the deployer controller will pick up the new resource.
			resourceObjNotReady.Status.Component.Version = componentVersion
			resourceObjNotReady.Status.Component.RepositorySpec = &apiextensionsv1.JSON{Raw: specData}
			resourceObjNotReady.Status.Resource.Version = resourceVersion
			hashRgd = sha256.Sum256(rgdUpdated)
			resourceObjNotReady.Status.Resource.Digest = fmt.Sprintf("SHA-256:%s[%s]", hex.EncodeToString(hashRgd[:]), "genericBlobDigest/v1")
			status.MarkReady(recorder, resourceObjNotReady, "updated mock resource")
			Expect(k8sClient.Status().Update(ctx, resourceObjNotReady)).To(Succeed())

			By("checking that the deployer gets reconciled again")
			test.WaitForReadyObject(ctx, k8sClient, deployerObj, map[string]any{})
			rgdObjUpdated := &krov1alpha1.ResourceGraphDefinition{}
			Eventually(func(g Gomega, ctx context.Context) {
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(rgdObj), rgdObjUpdated))
				g.Expect(rgdObjUpdated.Spec).To(Equal(rgdObjApplied.Spec))
			}, "15s").WithContext(ctx).Should(Succeed())

			By("mocking the GC")
			test.DeleteObject(ctx, k8sClient, rgdObj)

			By("deleting the deployer")
			test.DeleteObject(ctx, k8sClient, deployerObj)
		})

		It("fails when the resource is updated with an invalid change", func(ctx SpecContext) {
			By("creating a CTF")
			resourceType := artifacttypes.PLAIN_TEXT
			resourceVersion := "1.0.0"
			env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version(componentVersion, func() {
						env.Resource(resourceName, resourceVersion, resourceType, ocmmetav1.LocalRelation, func() {
							env.BlobData(mime.MIME_TEXT, rgd)
						})
					})
				})
			})

			spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, filepath.Join(tempDir, ctfName))
			Expect(err).NotTo(HaveOccurred())
			specData, err := spec.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())

			By("mocking a resource")
			hashRgd := sha256.Sum256(rgd)
			resourceObj = test.MockResource(
				ctx,
				resourceName,
				namespace.GetName(),
				&test.MockResourceOptions{
					ComponentRef: corev1.LocalObjectReference{
						Name: componentName,
					},
					Clnt:     k8sClient,
					Recorder: recorder,
					ComponentInfo: &v1alpha1.ComponentInfo{
						Component:      componentName,
						Version:        componentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
					},
					ResourceInfo: &v1alpha1.ResourceInfo{
						Name:    resourceName,
						Type:    resourceType,
						Version: resourceVersion,
						Access:  apiextensionsv1.JSON{Raw: []byte("{}")},
						// TODO: Consider calculating the digest the ocm-way
						Digest: fmt.Sprintf("SHA-256:%s[%s]", hex.EncodeToString(hashRgd[:]), "genericBlobDigest/v1"),
					},
				},
			)

			By("creating a deployer")
			deployerObj := &v1alpha1.Deployer{
				ObjectMeta: metav1.ObjectMeta{
					Name: deployerObjName,
				},
				Spec: v1alpha1.DeployerSpec{
					ResourceRef: v1alpha1.ObjectKey{
						Name:      resourceObj.GetName(),
						Namespace: namespace.GetName(),
					},
				},
			}
			Expect(k8sClient.Create(ctx, deployerObj)).To(Succeed())

			By("checking that the deployer has been reconciled successfully")
			test.WaitForReadyObject(ctx, k8sClient, deployerObj, map[string]any{})

			By("checking that the deployed ResourceGraphDefinition is correct")
			rgdObjApplied := &krov1alpha1.ResourceGraphDefinition{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(rgdObj), rgdObjApplied)).To(Succeed())
			Expect(rgdObjApplied.Spec).To(Equal(rgdObj.Spec))

			By("updating the mocked resource")
			componentVersion = "1.0.1"
			resourceVersion = "1.0.1"
			Expect(err).NotTo(HaveOccurred())
			env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version(componentVersion, func() {
						env.Resource(resourceName, resourceVersion, resourceType, ocmmetav1.LocalRelation, func() {
							env.BlobData(mime.MIME_TEXT, []byte("invalid-rgd"))
						})
					})
				})
			})

			spec, err = ctf.NewRepositorySpec(ctf.ACC_READONLY, filepath.Join(tempDir, ctfName))
			Expect(err).NotTo(HaveOccurred())
			specData, err = spec.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())

			By("updating the mocked resource")
			resourceObjNotReady := &v1alpha1.Resource{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(resourceObj), resourceObjNotReady)).To(Succeed())
			status.MarkNotReady(recorder, resourceObjNotReady, v1alpha1.ResourceIsNotAvailable, "mock resource is not ready")
			Expect(k8sClient.Status().Update(ctx, resourceObjNotReady)).To(Succeed())

			// All these field must be updated as the deployer controller will pick up the new resource.
			resourceObjNotReady.Status.Component.Version = componentVersion
			resourceObjNotReady.Status.Component.RepositorySpec = &apiextensionsv1.JSON{Raw: specData}
			resourceObjNotReady.Status.Resource.Version = resourceVersion
			hashRgd = sha256.Sum256([]byte("invalid-rgd"))
			resourceObjNotReady.Status.Resource.Digest = fmt.Sprintf("SHA-256:%s[%s]", hex.EncodeToString(hashRgd[:]), "genericBlobDigest/v1")
			status.MarkReady(recorder, resourceObjNotReady, "updated mock resource")
			Expect(k8sClient.Status().Update(ctx, resourceObjNotReady)).To(Succeed())

			By("checking that the deployer gets reconciled again and fails")
			test.WaitForNotReadyObject(ctx, k8sClient, deployerObj, v1alpha1.MarshalFailedReason)

			By("mocking the GC")
			test.DeleteObject(ctx, k8sClient, rgdObj)

			By("deleting the deployer")
			test.DeleteObject(ctx, k8sClient, deployerObj)
		})
	})
})

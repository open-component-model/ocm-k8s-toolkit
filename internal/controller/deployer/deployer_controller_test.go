package deployer

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"path/filepath"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/projectionfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	. "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"
	ocmmetav1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/extensions/artifacttypes"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/utils/accessio"
	"ocm.software/ocm/api/utils/mime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
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

		AfterEach(func() {
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
		})

		It("reconciles a created deployer", func(ctx SpecContext) {
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
						Access:  apiextensionsv1.JSON{[]byte("{}")},
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

			By("checking that the resource has been reconciled successfully")
			waitUntilDeployerIsReady(ctx, deployerObj)

			By("deleting the resource")
			test.DeleteObject(ctx, k8sClient, deployerObj)
		})

		PIt("fails when the resource digest differs", func() {})
		PIt("removes the resource when deleted", func() {})
	})
})

func waitUntilDeployerIsReady(ctx context.Context, deployer *v1alpha1.Deployer) {
	GinkgoHelper()

	Eventually(func(g Gomega, ctx context.Context) error {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(deployer), deployer)
		if err != nil {
			return err
		}

		if !conditions.IsReady(deployer) {
			return fmt.Errorf("deployer not ready")
		}

		return nil
	}, "15s").WithContext(ctx).Should(Succeed())
}

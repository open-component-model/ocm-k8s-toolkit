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
		env     *Builder
		ctfPath string
	)

	BeforeEach(func() {
		ctfPath = GinkgoT().TempDir()
		env = NewBuilder(environment.FileSystem(osfs.OsFs))
	})
	AfterEach(func() {
		Expect(env.Cleanup()).To(Succeed())
	})

	Context("resource controller", func() {
		var componentObj *v1alpha1.Component
		var namespace *corev1.Namespace
		var componentName, componentObjName, resourceName string
		repositoryName := "ocm-repository.io"

		BeforeEach(func(ctx SpecContext) {
			componentObjName = test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			componentName = "ocm.software/test-component-" + test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			resourceName = "resource"

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

		It("should reconcile a created resource", func(ctx SpecContext) {
			By("creating a component version with a resourceObj: plain text")
			componentVersion := "1.0.0"
			resourceVersion := "1.0.0"
			resourceType := artifacttypes.PLAIN_TEXT
			env.OCMCommonTransport(ctfPath, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version(componentVersion, func() {
						env.Resource(resourceName, resourceVersion, resourceType, v1.LocalRelation, func() {
							env.BlobData(mime.MIME_TEXT, []byte("Hello World!"))
						})
					})
				})
			})
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

			By("deleting the resource")
			deleteResource(ctx, resourceObj)
		})

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

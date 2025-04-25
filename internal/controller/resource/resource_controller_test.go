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
	"k8s.io/apimachinery/pkg/api/errors"
	. "ocm.software/ocm/api/helper/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	environment "ocm.software/ocm/api/helper/env"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/test"
)

const (
	RepositoryObj    = "test-repository"
	ComponentObj     = "test-component"
	ResourceObj      = "test-resource"
	ComponentVersion = "1.0.0"
	ResourceVersion  = "1.0.0"
	ResourceContent  = "some important content"
)

var _ = Describe("Resource Controller", func() {
	var (
		env *Builder
	)

	Context("resource controller", func() {
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
			// TODO: Add tests
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

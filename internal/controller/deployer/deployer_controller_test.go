package deployer

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

var _ = Describe("Deployer Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		deployer := &deliveryv1alpha1.Deployer{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Deployer")
			err := k8sClient.Get(ctx, typeNamespacedName, deployer)
			if err != nil && errors.IsNotFound(err) {
				resource := &deliveryv1alpha1.Deployer{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					// TODO(user): Specify other spec details if needed.
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &deliveryv1alpha1.Deployer{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Deployer")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
	})
})

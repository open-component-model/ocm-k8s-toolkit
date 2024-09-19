package controller

import (
	"context"
	"time"

	. "github.com/mandelsoft/goutils/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ocireg "ocm.software/ocm/api/ocm/extensions/repositories/ocireg"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

const (
	TestNamespaceOCMRepo = "test-namespace-ocmrepository"
	TestOCMRepositoryObj = "test-ocmrepository"
)

var _ = Describe("OCMRepository Controller", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
	})

	Context("Reconcile", func() {
		It("should reconcile the OCMRepository", func() {

			By("creating namespace object")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: TestNamespaceOCMRepo,
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

			By("creating a repository object")
			spec := ocireg.NewRepositorySpec("http://127.0.0.1:5000/ocm")
			specdata := Must(spec.MarshalJSON())
			ocmRepo := &v1alpha1.OCMRepository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: TestNamespaceOCMRepo,
					Name:      TestOCMRepositoryObj,
				},
				Spec: v1alpha1.OCMRepositorySpec{
					RepositorySpec: &apiextensionsv1.JSON{
						Raw: specdata,
					},
					Interval: metav1.Duration{Duration: time.Minute * 10},
				},
			}
			Expect(k8sClient.Create(ctx, ocmRepo)).To(Succeed())

			By("check that repository status has been created successfully")
			Eventually(komega.Object(ocmRepo), "1m").Should(
				HaveField("Status.RepositorySpec", Not(BeNil())))
		})
	})
})

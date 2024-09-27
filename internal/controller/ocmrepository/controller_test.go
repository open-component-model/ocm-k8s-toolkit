package ocmrepository

import (
	"context"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	. "github.com/mandelsoft/goutils/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ocireg "ocm.software/ocm/api/ocm/extensions/repositories/ocireg"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

const TestOCMRepositoryObj = "test-ocmrepository"

var _ = Describe("OCMRepository Controller", func() {
	var (
		ctx     context.Context
		cancel  context.CancelFunc
		ocmRepo *v1alpha1.OCMRepository
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
	})

	AfterEach(func() {
		_ = k8sClient.Delete(ctx, ocmRepo)
	})

	Describe("Reconsiling with different RepositorySpec specifications", func() {

		Context("When correct RepositorySpec is provided", func() {
			It("OCMRepository can be reconciled", func() {

				By("creating a OCI repository with existing host")
				spec := ocireg.NewRepositorySpec("ghcr.io/open-component-model")
				specdata := Must(spec.MarshalJSON())
				ocmRepo = newTestOCMRepository(TestNamespaceOCMRepo, TestOCMRepositoryObj, &specdata)
				Expect(k8sClient.Create(ctx, ocmRepo)).To(Succeed())

				By("check that repository status has been updated successfully")
				Eventually(komega.Object(ocmRepo), "1m").Should(And(
					HaveField("Status.RepositorySpec.Raw", Equal(specdata)),
					HaveField("Status.Conditions", ContainElement(
						And(HaveField("Type", Equal(meta.ReadyCondition)), HaveField("Status", Equal(metav1.ConditionTrue))),
					)),
				))
			})
		})

		Context("When incorrect RepositorySpec is provided", func() {
			It("Validation must fail", func() {

				By("creating a OCI repository with non-existing host")
				spec := ocireg.NewRepositorySpec("https://doesnotexist")
				specdata := Must(spec.MarshalJSON())
				ocmRepo = newTestOCMRepository(TestNamespaceOCMRepo, TestOCMRepositoryObj, &specdata)
				Expect(k8sClient.Create(ctx, ocmRepo)).To(Succeed())

				By("check that repository status has NOT been updated successfully")
				Eventually(komega.Object(ocmRepo), "1m").Should(And(
					HaveField("Status.RepositorySpec", BeNil()),
					HaveField("Status.Conditions", ContainElement(
						And(HaveField("Type", Equal(meta.ReadyCondition)), HaveField("Status", Equal(metav1.ConditionFalse))),
					)),
				))
			})
		})

		Context("When incorrect RepositorySpec is provided", func() {
			It("Validation must fail", func() {

				By("creating a OCI repository from invalid json")
				specdata := []byte("not a json")
				ocmRepo = newTestOCMRepository(TestNamespaceOCMRepo, TestOCMRepositoryObj, &specdata)
				Expect(k8sClient.Create(ctx, ocmRepo)).NotTo(Succeed())
			})
		})

		Context("When incorrect RepositorySpec is provided", func() {
			It("Validation must fail", func() {

				By("creating a OCI repository from a valid json but invalid RepositorySpec")
				specdata := []byte(`{"json":"not a valid RepositorySpec"}`)
				ocmRepo = newTestOCMRepository(TestNamespaceOCMRepo, TestOCMRepositoryObj, &specdata)
				Expect(k8sClient.Create(ctx, ocmRepo)).To(Succeed())

				By("check that repository status has NOT been updated successfully")
				Eventually(komega.Object(ocmRepo), "1m").Should(And(
					HaveField("Status.RepositorySpec", BeNil()),
					HaveField("Status.Conditions", ContainElement(
						And(HaveField("Type", Equal(meta.ReadyCondition)), HaveField("Status", Equal(metav1.ConditionFalse))),
					)),
				))
			})
		})
	})

	Describe("Reconsiling a valid OCMRepository", func() {

		Context("When SecretRefs and ConfigRefs properly set", func() {
			It("OCMRepository can be reconciled", func() {

				By("creating a OCI repository")
				spec := ocireg.NewRepositorySpec("ghcr.io/open-component-model")
				specdata := Must(spec.MarshalJSON())
				ocmRepo = newTestOCMRepository(TestNamespaceOCMRepo, TestOCMRepositoryObj, &specdata)

				By("adding SecretRefs")
				ocmRepo.Spec.SecretRef = &v1.LocalObjectReference{Name: secrets[0].Name}
				ocmRepo.Spec.SecretRefs = append(ocmRepo.Spec.SecretRefs, v1.LocalObjectReference{Name: secrets[1].Name})
				ocmRepo.Spec.SecretRefs = append(ocmRepo.Spec.SecretRefs, v1.LocalObjectReference{Name: secrets[2].Name})

				By("adding ConfigRefs")
				ocmRepo.Spec.ConfigRef = &v1.LocalObjectReference{Name: configs[0].Name}
				ocmRepo.Spec.ConfigRefs = append(ocmRepo.Spec.ConfigRefs, v1.LocalObjectReference{Name: configs[1].Name})
				ocmRepo.Spec.ConfigRefs = append(ocmRepo.Spec.ConfigRefs, v1.LocalObjectReference{Name: configs[2].Name})

				By("adding ConfigSet")
				configSet := "set1"
				ocmRepo.Spec.ConfigSet = &configSet

				By("creating OCMRepository object")
				Expect(k8sClient.Create(ctx, ocmRepo)).To(Succeed())

				By("check that the SecretRefs and ConfigRefs are in the status")
				Eventually(komega.Object(ocmRepo), "1m").Should(And(
					HaveField("Status.RepositorySpec.Raw", Equal(specdata)),
					HaveField("Status.Conditions", ContainElement(
						And(HaveField("Type", Equal(meta.ReadyCondition)), HaveField("Status", Equal(metav1.ConditionTrue))),
					)),
					HaveField("Status.SecretRefs", ContainElement(Equal(*ocmRepo.Spec.SecretRef))),
					HaveField("Status.SecretRefs", ContainElement(Equal(ocmRepo.Spec.SecretRefs[0]))),
					HaveField("Status.SecretRefs", ContainElement(Equal(ocmRepo.Spec.SecretRefs[1]))),
					HaveField("Status.ConfigRefs", ContainElement(Equal(*ocmRepo.Spec.ConfigRef))),
					HaveField("Status.ConfigRefs", ContainElement(Equal(ocmRepo.Spec.ConfigRefs[0]))),
					HaveField("Status.ConfigRefs", ContainElement(Equal(ocmRepo.Spec.ConfigRefs[1]))),
					HaveField("Status.ConfigSet", Equal(*ocmRepo.Spec.ConfigSet)),
				))
			})
		})
	})
})

func newTestOCMRepository(ns, name string, specdata *[]byte) *v1alpha1.OCMRepository {
	return &v1alpha1.OCMRepository{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
		},
		Spec: v1alpha1.OCMRepositorySpec{
			RepositorySpec: &apiextensionsv1.JSON{
				Raw: *specdata,
			},
			Interval: metav1.Duration{Duration: time.Minute * 10},
		},
	}
}

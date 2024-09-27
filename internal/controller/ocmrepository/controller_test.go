package ocmrepository

import (
	"context"
	"os"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/mandelsoft/goutils/testutils"
	"github.com/mandelsoft/vfs/pkg/osfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	. "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/ocm/extensions/repositories/ocireg"
	"ocm.software/ocm/api/utils/accessio"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

const (
	CTFPath              = "ocm-k8s-ctfstore--*"
	TestOCMRepositoryObj = "test-ocmrepository"
)

var _ = Describe("OCMRepository Controller", func() {
	var (
		ctx     context.Context
		cancel  context.CancelFunc
		ocmRepo *v1alpha1.OCMRepository
		env     *Builder
	)

	BeforeEach(func() {
		env = NewBuilder(environment.FileSystem(osfs.OsFs))
		DeferCleanup(env.Cleanup)

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
				repoName := TestOCMRepositoryObj + "-passing"
				ocmRepo = newTestOCMRepository(TestNamespaceOCMRepo, repoName, &specdata)
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
				repoName := TestOCMRepositoryObj + "-no-host"
				ocmRepo = newTestOCMRepository(TestNamespaceOCMRepo, repoName, &specdata)
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
				repoName := TestOCMRepositoryObj + "-invalid-json"
				ocmRepo = newTestOCMRepository(TestNamespaceOCMRepo, repoName, &specdata)
				Expect(k8sClient.Create(ctx, ocmRepo)).NotTo(Succeed())
			})
		})

		Context("When incorrect RepositorySpec is provided", func() {
			It("Validation must fail", func() {

				By("creating a OCI repository from a valid json but invalid RepositorySpec")
				specdata := []byte(`{"json":"not a valid RepositorySpec"}`)
				repoName := TestOCMRepositoryObj + "-invalid-spec"
				ocmRepo = newTestOCMRepository(TestNamespaceOCMRepo, repoName, &specdata)
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
				repoName := TestOCMRepositoryObj + "-all-fields"
				ocmRepo = newTestOCMRepository(TestNamespaceOCMRepo, repoName, &specdata)

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

		Context("repository controller", func() {
			It("reconciles a repository", func() {
				By("creating a repository object")
				ctfpath := Must(os.MkdirTemp("", CTFPath))
				componentName := "ocm.software/test-component"
				componentVersion := "v1.0.0"
				env.OCMCommonTransport(ctfpath, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(componentVersion)
					})
				})
				spec := Must(ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfpath))
				specdata := Must(spec.MarshalJSON())
				ocmRepoName := TestOCMRepositoryObj + "-deleted"
				ocmRepo = newTestOCMRepository(TestNamespaceOCMRepo, ocmRepoName, &specdata)

				Expect(k8sClient.Create(ctx, ocmRepo)).To(Succeed())

				By("checking if the repository is ready")

				Eventually(func() bool {
					Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: TestNamespaceOCMRepo, Name: ocmRepoName}, ocmRepo)).To(Succeed())
					return conditions.IsReady(ocmRepo)
				}).WithTimeout(5 * time.Second).Should(BeTrue())

				By("creating a component that uses this repository")
				component := &v1alpha1.Component{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: TestNamespaceOCMRepo,
						Name:      "test-component-name",
					},
					Spec: v1alpha1.ComponentSpec{
						RepositoryRef: v1alpha1.ObjectKey{
							Namespace: TestNamespaceOCMRepo,
							Name:      ocmRepoName,
						},
						Component:              componentName,
						EnforceDowngradability: false,
						Semver:                 "1.0.0",
						Interval:               metav1.Duration{Duration: time.Minute * 10},
					},
					Status: v1alpha1.ComponentStatus{},
				}
				Expect(k8sClient.Create(ctx, component)).To(Succeed())
				By("deleting the repository should not allow the deletion unless the component is removed")
				Expect(k8sClient.Delete(ctx, ocmRepo)).To(Succeed())
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: TestNamespaceOCMRepo, Name: ocmRepoName}, ocmRepo)).To(Succeed())

				By("removing the component")
				Expect(k8sClient.Delete(ctx, component)).To(Succeed())

				By("checking if the repository is eventually deleted")
				Eventually(func() error {
					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: TestNamespaceOCMRepo, Name: ocmRepoName}, ocmRepo)
					if errors.IsNotFound(err) {
						return nil
					}

					return err
				}).WithTimeout(10 * time.Second).Should(Succeed())
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

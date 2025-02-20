package configuration

import (
	"context"
	"os"
	"path/filepath"
	"time"

	_ "embed"

	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/mandelsoft/goutils/testutils"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/projectionfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ocmbuilder "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/compression"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/test"
)

const (
	Namespace          = "test-namespace"
	ResourceConfig     = "cfg-test-util"
	TargetResourceObj  = "target-test-util"
	ConfiguredResource = "configured-resource"
)

var _ = Describe("ConfiguredResource Controller", func() {
	var (
		tmp string
		env *ocmbuilder.Builder
	)

	BeforeEach(func() {
		tmp = GinkgoT().TempDir()
		testfs, err := projectionfs.New(osfs.New(), tmp)
		Expect(err).ToNot(HaveOccurred())
		env = ocmbuilder.NewBuilder(environment.FileSystem(testfs))
		DeferCleanup(env.Cleanup)
	})

	BeforeEach(func(ctx SpecContext) {
		By("creating namespace object")
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: Namespace,
			},
		}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
	})

	It("should configure an artifact from a resource based on a ResourceConfig", func(ctx SpecContext) {
		By("creating a mock component")
		component := NoOpComponent(ctx)

		By("creating a mock target resource")
		fileToConfigure := "test.yaml"
		fileContentBeforeConfiguration := []byte(`mykey: "value"`)
		fileContentAfterConfiguration := []byte(`mykey: "substituted"`)

		dir := filepath.Join(tmp, "test")
		Expect(os.Mkdir(dir, os.ModePerm|os.ModeDir)).To(Succeed())

		path := filepath.Join(dir, fileToConfigure)

		writer := Must(os.OpenFile(path, os.O_CREATE|os.O_RDWR, os.ModePerm))
		defer func() {
			Expect(writer.Close()).To(Succeed())
		}()

		Must(writer.Write(fileContentBeforeConfiguration))

		targetResource := test.SetupMockResourceWithData(ctx,
			TargetResourceObj,
			Namespace,
			&test.MockResourceOptions{
				DataPath: dir,
				ComponentRef: v1alpha1.ObjectKey{
					Namespace: Namespace,
					Name:      component.GetName(),
				},
				Registry: registry,
				Clnt:     k8sClient,
				Recorder: recorder,
			},
		)

		Eventually(func(ctx context.Context) bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(targetResource), targetResource)
			if err != nil {
				return false
			}
			return conditions.IsReady(targetResource)
		}, "15s").WithContext(ctx).Should(BeTrue())

		By("creating a resource config")
		cfg := v1alpha1.ResourceConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ResourceConfig,
				Namespace: Namespace,
			},
			Spec: v1alpha1.ResourceConfigSpec{
				Rules: []v1alpha1.ConfigurationRule{
					{
						YAMLSubstitution: &v1alpha1.ConfigurationRuleYAMLSubstitution{
							Target: v1alpha1.ConfigurationRuleYAMLSubsitutionTarget{
								File: v1alpha1.FileTargetWithValue{
									FileTarget: v1alpha1.FileTarget{
										Path: fileToConfigure,
									},
									Value: "mykey",
								},
							},
							Source: v1alpha1.ConfigurationRuleYAMLSubsitutionSource{
								Value: "substituted",
							},
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, &cfg)).To(Succeed())

		By("creating a configured resource")
		configuredResource := &v1alpha1.ConfiguredResource{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ConfiguredResource,
				Namespace: Namespace,
			},
			Spec: v1alpha1.ConfiguredResourceSpec{
				Target:   v1alpha1.ResourceToConfigurationReference(targetResource),
				Config:   v1alpha1.ResourceConfigToConfigurationReference(&cfg),
				Interval: metav1.Duration{Duration: 10 * time.Minute},
			},
		}
		Expect(k8sClient.Create(ctx, configuredResource)).To(Succeed())

		Eventually(func(ctx context.Context) bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(configuredResource), configuredResource)
			if err != nil {
				return false
			}
			return conditions.IsReady(configuredResource) && configuredResource.GetSnapshotName() != ""
		}, "15s").WithContext(ctx).Should(BeTrue())

		snapshotResource := &v1alpha1.Snapshot{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: configuredResource.GetNamespace(), Name: configuredResource.GetSnapshotName()}, snapshotResource)).To(Succeed())

		snapshotRepository, err := registry.NewRepository(ctx, snapshotResource.Spec.Repository)
		Expect(err).NotTo(HaveOccurred())
		snapshotResourceContent, err := snapshotRepository.FetchSnapshot(ctx, snapshotResource.GetDigest())
		Expect(err).NotTo(HaveOccurred())
		dataExtracted, err := compression.ExtractDataFromTGZ(snapshotResourceContent)
		Expect(err).NotTo(HaveOccurred())
		Expect(dataExtracted).To(MatchYAML(fileContentAfterConfiguration))

		By("delete resources manually")
		Expect(k8sClient.Delete(ctx, configuredResource)).To(Succeed())
		Expect(k8sClient.Delete(ctx, snapshotResource)).To(Succeed())
		Eventually(func(ctx context.Context) bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(configuredResource), configuredResource)
			return errors.IsNotFound(err)
		}, "15s").WithContext(ctx).Should(BeTrue())

		Expect(k8sClient.Delete(ctx, &cfg)).To(Succeed())
		Eventually(func(ctx context.Context) bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(&cfg), &cfg)
			return errors.IsNotFound(err)
		}, "15s").WithContext(ctx).Should(BeTrue())

		Expect(k8sClient.Delete(ctx, targetResource)).To(Succeed())
		snapshotTargetResource := &v1alpha1.Snapshot{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: targetResource.GetNamespace(), Name: targetResource.GetSnapshotName()}, snapshotTargetResource)).To(Succeed())
		Expect(k8sClient.Delete(ctx, snapshotTargetResource)).To(Succeed())

		Expect(k8sClient.Delete(ctx, component)).To(Succeed())
		snapshotComponent := &v1alpha1.Snapshot{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: component.GetNamespace(), Name: component.GetSnapshotName()}, snapshotComponent)).To(Succeed())
		Expect(k8sClient.Delete(ctx, snapshotComponent)).To(Succeed())
	})
})

func NoOpComponent(ctx context.Context) *v1alpha1.Component {
	component := test.SetupComponentWithDescriptorList(ctx,
		"any-component-that-should-not-be-introspected",
		Namespace,
		[]byte("noop"),
		&test.MockComponentOptions{
			Registry: registry,
			Client:   k8sClient,
			Recorder: recorder,
			Info: v1alpha1.ComponentInfo{
				Component:      "acme.org/test",
				Version:        "1.0.0",
				RepositorySpec: &apiextensionsv1.JSON{Raw: []byte(`{}`)},
			},
			Repository: "repo-that-should-not-be-introspected",
		},
	)

	return component
}

package configuration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "embed"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/tar"
	. "github.com/mandelsoft/goutils/testutils"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/projectionfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ocmbuilder "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/test"
)

const (
	ResourceConfig     = "cfg-configuration-util"
	TargetResourceObj  = "target-configuration-util"
	ConfiguredResource = "configured-resource"
)

var _ = Describe("ConfiguredResource Controller", func() {
	var (
		tmp, namespaceName string
		env                *ocmbuilder.Builder

		componentObj   *v1alpha1.Component
		targetResource *v1alpha1.Resource
	)

	BeforeEach(func() {
		tmp = GinkgoT().TempDir()
		testFs, err := projectionfs.New(osfs.New(), tmp)
		Expect(err).ToNot(HaveOccurred())
		env = ocmbuilder.NewBuilder(environment.FileSystem(testFs))
		DeferCleanup(env.Cleanup)
	})

	BeforeEach(func(ctx SpecContext) {
		namespaceName = test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
		namespace := &corev1.Namespace{
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
			if errors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}

			return fmt.Errorf("expected not-found error, but got none")
		}, "15s").WithContext(ctx).Should(Succeed())

		By("deleting the target resource")
		Expect(k8sClient.Delete(ctx, targetResource)).To(Succeed())
		Eventually(func(ctx context.Context) error {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(targetResource), targetResource)
			if errors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}

			return fmt.Errorf("expected not-found error, but got none")
		}, "15s").WithContext(ctx).Should(Succeed())

		confResources := &v1alpha1.ConfiguredResourceList{}
		Expect(k8sClient.List(ctx, confResources, client.InNamespace(namespaceName))).To(Succeed())
		Expect(confResources.Items).To(HaveLen(0))

		resConfig := &v1alpha1.ResourceConfigList{}
		Expect(k8sClient.List(ctx, resConfig, client.InNamespace(namespaceName))).To(Succeed())
		Expect(resConfig.Items).To(HaveLen(0))
	})

	It("should configure an artifact from a resource based on a ResourceConfig", func(ctx SpecContext) {
		By("creating a mock component")
		componentObj = NoOpComponent(ctx, namespaceName)

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

		targetResource = test.SetupMockResourceWithData(ctx,
			TargetResourceObj,
			namespaceName,
			&test.MockResourceOptions{
				DataPath: dir,
				ComponentRef: v1alpha1.ObjectKey{
					Namespace: namespaceName,
					Name:      componentObj.GetName(),
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
				Namespace: namespaceName,
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
				Namespace: namespaceName,
			},
			Spec: v1alpha1.ConfiguredResourceSpec{
				Target:   v1alpha1.ResourceToConfigurationReference(targetResource),
				Config:   v1alpha1.ResourceConfigToConfigurationReference(&cfg),
				Interval: metav1.Duration{Duration: 10 * time.Minute},
			},
		}
		Expect(k8sClient.Create(ctx, configuredResource)).To(Succeed())

		Eventually(func(ctx context.Context) error {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(configuredResource), configuredResource)
			if err != nil {
				return err
			}
			if !conditions.IsReady(configuredResource) {
				return fmt.Errorf("resource not ready")
			}
			if configuredResource.GetOCIArtifact() == nil {
				return fmt.Errorf("OCI artifact not present")
			}
			return nil
		}, "15s").WithContext(ctx).Should(Succeed())

		ociRepository, err := registry.NewRepository(ctx, configuredResource.GetOCIRepository())
		Expect(err).NotTo(HaveOccurred())
		resourceContentTGZ, err := ociRepository.FetchArtifact(ctx, configuredResource.GetManifestDigest())
		Expect(err).NotTo(HaveOccurred())
		tmpArtifact := filepath.Join(tmp, "artifact")
		Expect(os.Mkdir(tmpArtifact, os.ModePerm|os.ModeDir)).To(Succeed())
		Expect(tar.Untar(bytes.NewReader(resourceContentTGZ), tmpArtifact)).To(Succeed())
		content, err := os.ReadFile(filepath.Join(tmpArtifact, fileToConfigure))
		Expect(err).NotTo(HaveOccurred())
		Expect(content).To(MatchYAML(fileContentAfterConfiguration))

		By("delete resources manually")
		Expect(k8sClient.Delete(ctx, configuredResource)).To(Succeed())
		Eventually(func(ctx context.Context) error {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(configuredResource), configuredResource)
			if errors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}

			return fmt.Errorf("expected not-found error, but got none")
		}, "15s").WithContext(ctx).Should(Succeed())
		test.ExpectArtifactToNotExist(ctx, registry, configuredResource.GetOCIArtifact())

		Expect(k8sClient.Delete(ctx, &cfg)).To(Succeed())
		Eventually(func(ctx context.Context) error {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(&cfg), &cfg)
			if errors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}

			return fmt.Errorf("expected not-found error, but got none")
		}, "15s").WithContext(ctx).Should(Succeed())
	})
})

func NoOpComponent(ctx context.Context, namespaceName string) *v1alpha1.Component {
	component := test.SetupComponentWithDescriptorList(ctx,
		"any-component-that-should-not-be-introspected",
		namespaceName,
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

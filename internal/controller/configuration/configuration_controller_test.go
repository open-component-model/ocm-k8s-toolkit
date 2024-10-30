package configuration

import (
	"context"
	"path/filepath"
	"time"

	_ "embed"

	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/projectionfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ocmbuilder "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
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
		component := NoOpComponent(ctx, tmp)

		fileToConfigure := "test.yaml"
		fileContentBeforeConfiguration := []byte(`mykey: "value"`)
		fileContentAfterConfiguration := []byte(`mykey: "substituted"`)

		dir := filepath.Join(tmp, "test")
		test.CreateTGZ(dir, map[string][]byte{
			fileToConfigure: fileContentBeforeConfiguration,
		})

		targetResource := test.SetupMockResourceWithData(ctx,
			TargetResourceObj,
			Namespace,
			&test.MockResourceOptions{
				BasePath: tmp,
				DataPath: dir,
				ComponentRef: v1alpha1.ObjectKey{
					Namespace: Namespace,
					Name:      component.GetName(),
				},
				Strg:     strg,
				Clnt:     k8sClient,
				Recorder: recorder,
			},
		)
		DeferCleanup(func(ctx SpecContext) {
			Expect(k8sClient.Delete(ctx, targetResource, client.PropagationPolicy(metav1.DeletePropagationForeground))).To(Succeed())
		})

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
		DeferCleanup(func(ctx SpecContext) {
			Expect(k8sClient.Delete(ctx, &cfg, client.PropagationPolicy(metav1.DeletePropagationForeground))).To(Succeed())
		})

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
		DeferCleanup(func(ctx SpecContext) {
			Expect(k8sClient.Delete(ctx, configuredResource, client.PropagationPolicy(metav1.DeletePropagationForeground))).To(Succeed())
		})

		Eventually(Object(configuredResource), "15s").Should(
			HaveField("Status.ArtifactRef.Name", Not(BeEmpty())))

		art := &artifactv1.Artifact{}
		art.Name = configuredResource.Status.ArtifactRef.Name
		art.Namespace = configuredResource.Namespace

		test.VerifyArtifact(strg, art, map[string]func(data []byte){
			fileToConfigure: func(data []byte) {
				Expect(data).To(MatchYAML(fileContentAfterConfiguration))
			},
		})
	})

})

func NoOpComponent(ctx context.Context, basePath string) *v1alpha1.Component {
	component := test.SetupComponentWithDescriptorList(ctx,
		"any-component-that-should-not-be-introspected",
		Namespace,
		nil,
		&test.MockComponentOptions{
			BasePath: basePath,
			Strg:     strg,
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
	DeferCleanup(func(ctx SpecContext) {
		Expect(k8sClient.Delete(ctx, component, client.PropagationPolicy(metav1.DeletePropagationForeground))).To(Succeed())
	})
	return component
}

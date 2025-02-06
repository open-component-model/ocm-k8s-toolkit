package localization

import (
	"bytes"
	"context"
	"path/filepath"
	"text/template"

	_ "embed"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	"github.com/mandelsoft/vfs/pkg/memoryfs"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/projectionfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"ocm.software/ocm/api/utils/tarutils"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ocmbuilder "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/test"
)

var (
	//go:embed testdata/descriptor-list.yaml
	descriptorListYAML []byte
	//go:embed testdata/replaced-values.yaml
	replacedValuesYAML []byte
	//go:embed testdata/replaced-deployment.yaml
	replacedDeploymentYAML []byte
	//go:embed testdata/localization-config.yaml
	configYAML []byte
	//go:embed testdata/localized_resource_patch.yaml.tmpl
	localizationTemplateKustomizePatch string
)

const (
	Namespace         = "test-namespace"
	RepositoryObj     = "test-repository"
	ComponentObj      = "test-component"
	CfgResourceObj    = "cfg-test-util"
	TargetResourceObj = "target-test-util"
	Localization      = "test-localization"
)

var _ = Describe("Localization Controller", func() {
	var (
		tmp string
		env *ocmbuilder.Builder

		targetResource *v1alpha1.Resource
		cfgResource    *v1alpha1.Resource
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

	It("should localize an artifact from a resource based on a config supplied in a sibling resource", func(ctx SpecContext) {
		component := test.SetupComponentWithDescriptorList(ctx,
			ComponentObj,
			Namespace,
			descriptorListYAML,
			&test.MockComponentOptions{
				BasePath: tmp,
				Registry: registry,
				Client:   k8sClient,
				Recorder: recorder,
				Info: v1alpha1.ComponentInfo{
					Component:      "acme.org/test",
					Version:        "1.0.0",
					RepositorySpec: &apiextensionsv1.JSON{Raw: []byte(`{}`)},
				},
				Repository: RepositoryObj,
			},
		)
		DeferCleanup(func(ctx SpecContext) {
			Expect(k8sClient.Delete(ctx, component, client.PropagationPolicy(metav1.DeletePropagationForeground))).To(Succeed())
		})

		var err error
		targetResource = test.SetupMockResourceWithData(ctx,
			TargetResourceObj,
			Namespace,
			&test.MockResourceOptions{
				BasePath: tmp,
				DataPath: filepath.Join("testdata", "deployment-instruction-helm"),
				ComponentRef: v1alpha1.ObjectKey{
					Namespace: Namespace,
					Name:      ComponentObj,
				},
				Registry: registry,
				Clnt:     k8sClient,
				Recorder: recorder,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) {
			Expect(k8sClient.Delete(ctx, targetResource, client.PropagationPolicy(metav1.DeletePropagationForeground))).To(Succeed())
		})
		cfgResource = test.SetupMockResourceWithData(ctx,
			CfgResourceObj,
			Namespace,
			&test.MockResourceOptions{
				BasePath: tmp,
				Data:     bytes.NewReader(configYAML),
				ComponentRef: v1alpha1.ObjectKey{
					Namespace: Namespace,
					Name:      ComponentObj,
				},
				Registry: registry,
				Clnt:     k8sClient,
				Recorder: recorder,
			},
		)
		DeferCleanup(func(ctx SpecContext) {
			Expect(k8sClient.Delete(ctx, cfgResource, client.PropagationPolicy(metav1.DeletePropagationForeground))).To(Succeed())
		})

		localization := SetupLocalizedResource(ctx, map[string]string{
			"Namespace":          Namespace,
			"Name":               Localization,
			"TargetResourceName": targetResource.Name,
			"ConfigResourceName": cfgResource.Name,
		})
		DeferCleanup(func(ctx SpecContext) {
			Expect(k8sClient.Delete(ctx, localization, client.PropagationPolicy(metav1.DeletePropagationForeground))).To(Succeed())
		})

		Eventually(Object(localization), "15s").Should(
			HaveField("Status.SnapshotRef.Name", Not(BeEmpty())))

		snapshotCR := &v1alpha1.Snapshot{}
		snapshotCR.Name = localization.Status.SnapshotRef.Name
		snapshotCR.Namespace = localization.Namespace

		Eventually(Object(snapshotCR), "5s").Should(HaveField("Spec.URL", Not(BeEmpty())))

		repository, err := registry.NewRepository(ctx, snapshotCR.Spec.Repository)
		Expect(err).ToNot(HaveOccurred())

		data, err := repository.FetchSnapshot(ctx, snapshotCR.GetDigest())
		Expect(err).ToNot(HaveOccurred())

		memFs := vfs.New(memoryfs.New())
		Expect(tarutils.UnzipTarToFs(memFs, data)).To(Succeed())

		valuesData, err := memFs.ReadFile("values.yaml")
		Expect(err).ToNot(HaveOccurred())
		Expect(valuesData).To(MatchYAML(replacedValuesYAML))

		deploymentData, err := memFs.ReadFile(filepath.Join("templates", "deployment.yaml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(deploymentData).To(BeEquivalentTo(replacedDeploymentYAML))

	})
})

func SetupLocalizedResource(ctx context.Context, data map[string]string) *v1alpha1.LocalizedResource {
	localizationTemplate, err := template.New("localization").Parse(localizationTemplateKustomizePatch)
	Expect(err).ToNot(HaveOccurred())
	var ltpl bytes.Buffer
	Expect(localizationTemplate.ExecuteTemplate(&ltpl, "localization", data)).To(Succeed())
	localization := &v1alpha1.LocalizedResource{}
	serializer := serializer.NewCodecFactory(k8sClient.Scheme()).UniversalDeserializer()
	_, _, err = serializer.Decode(ltpl.Bytes(), nil, localization)
	Expect(err).To(Not(HaveOccurred()))
	Expect(k8sClient.Create(ctx, localization)).To(Succeed())
	return localization
}

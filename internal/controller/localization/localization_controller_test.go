package localization

import (
	"bytes"
	"context"
	"path/filepath"
	"text/template"

	_ "embed"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/mandelsoft/vfs/pkg/memoryfs"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/projectionfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	ocmbuilder "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"
	"ocm.software/ocm/api/utils/tarutils"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
		testFs, err := projectionfs.New(osfs.New(), tmp)
		Expect(err).ToNot(HaveOccurred())
		env = ocmbuilder.NewBuilder(environment.FileSystem(testFs))
		DeferCleanup(env.Cleanup)
	})

	It("should localize an snapshot from a resource based on a config supplied in a sibling resource", func(ctx SpecContext) {
		component := test.SetupComponentWithDescriptorList(ctx,
			ComponentObj,
			Namespace,
			descriptorListYAML,
			&test.MockComponentOptions{
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

		targetResource = test.SetupMockResourceWithData(ctx,
			TargetResourceObj,
			Namespace,
			&test.MockResourceOptions{
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

		cfgResource = test.SetupMockResourceWithData(ctx,
			CfgResourceObj,
			Namespace,
			&test.MockResourceOptions{
				Data: bytes.NewReader(configYAML),
				ComponentRef: v1alpha1.ObjectKey{
					Namespace: Namespace,
					Name:      ComponentObj,
				},
				Registry: registry,
				Clnt:     k8sClient,
				Recorder: recorder,
			},
		)

		localization := setupLocalizedResource(ctx, map[string]string{
			"Namespace":          Namespace,
			"Name":               Localization,
			"TargetResourceName": targetResource.Name,
			"ConfigResourceName": cfgResource.Name,
		})

		By("checking that the resource has been reconciled successfully")
		Eventually(func(ctx context.Context) bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(localization), localization)
			if err != nil {
				return false
			}
			return conditions.IsReady(localization) && localization.GetSnapshotName() != ""
		}, "15s").WithContext(ctx).Should(BeTrue())

		snapshotLocalization := &v1alpha1.Snapshot{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: localization.GetNamespace(), Name: localization.GetSnapshotName()}, snapshotLocalization)).To(Succeed())

		// Marks snapshot as ready
		conditions.MarkTrue(snapshotLocalization, "Ready", "ready", "message")
		Expect(k8sClient.Status().Update(ctx, snapshotLocalization)).To(Succeed())

		repository, err := registry.NewRepository(ctx, snapshotLocalization.Spec.Repository)
		Expect(err).ToNot(HaveOccurred())
		data, err := repository.FetchSnapshot(ctx, snapshotLocalization.GetDigest())
		Expect(err).ToNot(HaveOccurred())

		memFs := vfs.New(memoryfs.New())
		Expect(tarutils.UnzipTarToFs(memFs, bytes.NewReader(data))).To(Succeed())

		valuesData, err := memFs.ReadFile("values.yaml")
		Expect(err).ToNot(HaveOccurred())
		Expect(valuesData).To(MatchYAML(replacedValuesYAML))

		deploymentData, err := memFs.ReadFile(filepath.Join("templates", "deployment.yaml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(deploymentData).To(BeEquivalentTo(replacedDeploymentYAML))

		By("delete resources manually")
		Expect(k8sClient.Delete(ctx, localization)).To(Succeed())
		Expect(k8sClient.Delete(ctx, snapshotLocalization)).To(Succeed())
		Eventually(func(ctx context.Context) bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(localization), localization)
			return errors.IsNotFound(err)
		}, "15s").WithContext(ctx).Should(BeTrue())

		Expect(k8sClient.Delete(ctx, cfgResource)).To(Succeed())
		snapshotCfgResource := &v1alpha1.Snapshot{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cfgResource.GetNamespace(), Name: cfgResource.GetSnapshotName()}, snapshotCfgResource)).To(Succeed())
		Expect(k8sClient.Delete(ctx, snapshotCfgResource)).To(Succeed())

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

func setupLocalizedResource(ctx context.Context, data map[string]string) *v1alpha1.LocalizedResource {
	localizationTemplate, err := template.New("localization").Parse(localizationTemplateKustomizePatch)
	Expect(err).ToNot(HaveOccurred())
	var ltpl bytes.Buffer
	Expect(localizationTemplate.ExecuteTemplate(&ltpl, "localization", data)).To(Succeed())
	localization := &v1alpha1.LocalizedResource{}
	serializerFactory := serializer.NewCodecFactory(k8sClient.Scheme()).UniversalDeserializer()
	_, _, err = serializerFactory.Decode(ltpl.Bytes(), nil, localization)
	Expect(err).To(Not(HaveOccurred()))
	Expect(k8sClient.Create(ctx, localization)).To(Succeed())
	return localization
}

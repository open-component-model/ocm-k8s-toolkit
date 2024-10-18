package mapped

import (
	"os"
	"path/filepath"

	_ "embed"

	"github.com/mandelsoft/vfs/pkg/memoryfs"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/projectionfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"ocm.software/ocm/api/utils/tarutils"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	localizationclient "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/client"
	localizationTypes "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/types"
)

//go:embed testdata/descriptor-list.yaml
var descriptorListYAML []byte

//go:embed testdata/replaced-values.yaml
var replacedValuesYAML []byte

//go:embed testdata/replaced-deployment.yaml
var replacedDeploymentYAML []byte

//go:embed testdata/localization-config.yaml
var configYAML []byte

var _ = Describe("mapped localize", func() {
	Context("should localize components based on sibling resources in the component descriptor", func() {
		It("and map as well as successfully substitute correctly", func(ctx SpecContext) {
			tmp := GinkgoT().TempDir()

			var ls localizationTypes.LocalizationSourceWithStrategy

			By("reading in the localization config as source", func() {
				configDir := filepath.Join(tmp, "config")
				Expect(os.Mkdir(configDir, os.ModePerm|os.ModeDir))
				configPath := filepath.Join(configDir, "localization.yaml")
				Expect(os.WriteFile(configPath, configYAML, os.ModePerm)).To(Succeed())
				ls = localizationTypes.NewLocalizationSourceWithStrategy(
					&localizationTypes.MockedLocalizationReference{
						Resource: &v1alpha1.Resource{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "deployment-localization",
								Namespace: "default",
							},
							Spec: v1alpha1.ResourceSpec{
								ComponentRef: v1.LocalObjectReference{
									Name: "component",
								},
							},
						},
						Path: configPath,
					},
					v1alpha1.LocalizationStrategy{
						Mapped: &v1alpha1.LocalizationStrategyMapped{},
					},
				)
			})

			var trgt localizationTypes.MockedLocalizationReference

			By("accessing the deployment specification as target", func() {
				targetDir := filepath.Join(tmp, "target")
				Expect(os.Mkdir(targetDir, os.ModePerm|os.ModeDir))
				targetTGZ := filepath.Join(targetDir, "target.tgz")
				targetFS, err := projectionfs.New(osfs.New(), filepath.Join("testdata", "deployment-instruction-helm"))
				Expect(err).ToNot(HaveOccurred())
				Expect(tarutils.CreateTarFromFs(targetFS, targetTGZ, tarutils.Gzip)).To(Succeed())

				trgt = localizationTypes.MockedLocalizationReference{
					Path: targetTGZ,
					Resource: &v1alpha1.Resource{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "deployment-instructions",
							Namespace: "default",
						},
						Spec: v1alpha1.ResourceSpec{
							ComponentRef: v1.LocalObjectReference{
								Name: "component",
							},
						},
					},
				}
			})

			var objs []runtime.Object
			By("leveraging the introspective artifact access of component version lists in the referenced component", func() {
				descriptorDir := filepath.Join(tmp, "descriptor")
				Expect(os.Mkdir(descriptorDir, os.ModePerm|os.ModeDir))
				descriptorListTGZPath := filepath.Join(descriptorDir, "descriptor-list.tgz")
				descriptorArtifactFS := memoryfs.New()
				descriptorListWriter, err := descriptorArtifactFS.OpenFile(v1alpha1.OCMComponentDescriptorList, os.O_CREATE|os.O_RDWR, os.ModePerm)
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() error {
					return descriptorListWriter.Close()
				})
				_, err = descriptorListWriter.Write(descriptorListYAML)
				Expect(tarutils.CreateTarFromFs(descriptorArtifactFS, descriptorListTGZPath, tarutils.Gzip)).To(Succeed())
				descriptorListArtifactURL, err := filepath.Rel(tmp, descriptorListTGZPath)
				Expect(err).ToNot(HaveOccurred())

				objs = []runtime.Object{
					&v1alpha1.Component{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "component",
							Namespace: "default",
						},
						Spec: v1alpha1.ComponentSpec{
							Component: "acme.org/test",
						},
						Status: v1alpha1.ComponentStatus{
							ArtifactRef: v1.LocalObjectReference{
								Name: "component",
							},
							Component: v1alpha1.ComponentInfo{
								Version: "1.0.0",
							},
						},
					},
					&artifactv1.Artifact{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "component",
							Namespace: "default",
						},
						Spec: artifactv1.ArtifactSpec{
							URL: descriptorListArtifactURL,
						},
					},
				}
			})

			By("being able to reference and replace values in the target dynamically based on the component descriptor", func() {
				clnt := fake.NewFakeClient(objs...)

				strg, err := storage.NewStorage(clnt, scheme.Scheme, tmp, "localhost", 0, 0)
				Expect(err).ToNot(HaveOccurred())

				path, err := Localize(ctx, localizationclient.NewClientWithLocalStorage(clnt, strg), strg, ls, &trgt)
				Expect(err).ToNot(HaveOccurred())

				Expect(path).ToNot(BeEmpty())
				Expect(path).To(BeADirectory())
				Expect(filepath.Join(path, "values.yaml")).To(BeAnExistingFile())

				valuesData, err := os.ReadFile(filepath.Join(path, "values.yaml"))
				Expect(err).ToNot(HaveOccurred())
				Expect(valuesData).To(MatchYAML(replacedValuesYAML))

				deploymentData, err := os.ReadFile(filepath.Join(path, "templates", "deployment.yaml"))
				Expect(err).ToNot(HaveOccurred())
				Expect(deploymentData).To(BeEquivalentTo(replacedDeploymentYAML))
			})
		})
	})
})

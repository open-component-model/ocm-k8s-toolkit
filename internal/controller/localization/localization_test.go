package localization

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
	"time"

	_ "embed"

	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/mandelsoft/vfs/pkg/memoryfs"
	"github.com/mandelsoft/vfs/pkg/projectionfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	ocmbuilder "ocm.software/ocm/api/helper/builder"
	"ocm.software/ocm/api/utils/tarutils"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	//. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	"github.com/mandelsoft/vfs/pkg/osfs"
	environment "ocm.software/ocm/api/helper/env"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

const modeReadWriteUser = 0o600

//go:embed testdata/patch_test/deployment_patch_strategic_merge.yaml
var deploymentPatch []byte

//go:embed testdata/patch_test/deployment_patch_strategic_merge_result.yaml
var deploymentPatchResult []byte

//go:embed testdata/patch_test/deployment.yaml
var deployment []byte

//go:embed testdata/patch_test/localization_kustomize_patch.yaml.tmpl
var localizationTemplateKustomizePatch string

const (
	Namespace         = "test-namespace"
	RepositoryObj     = "test-repository"
	ComponentObj      = "test-component"
	SourceResourceObj = "source-test-resource"
	TargetResourceObj = "target-test-resource"
	Localization      = "test-localization"
)

var _ = Describe("LocalizationRules Controller", func() {
	var (
		tmp string
		env *ocmbuilder.Builder

		targetResource *v1alpha1.Resource
		sourceResource *v1alpha1.Resource
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

	It("should localize an artifact from a resource", func(ctx SpecContext) {

		targetResource = SetupMockResourceWithData(ctx,
			TargetResourceObj,
			Namespace,
			RepositoryObj,
			ComponentObj,
			options{
				filename: "deployment.yaml",
				data:     deployment,
				basePath: tmp,
			},
		)
		sourceResource = SetupMockResourceWithData(ctx,
			SourceResourceObj,
			Namespace,
			RepositoryObj,
			ComponentObj,
			options{
				filename: "deployment_patch.yaml",
				data:     deploymentPatch,
				basePath: tmp,
			},
		)

		localization := SetupKustomizePatchLocalization(map[string]string{
			"Namespace":          Namespace,
			"Name":               Localization,
			"TargetResourceName": targetResource.Name,
			"SourceResourceName": sourceResource.Name,
			"FilePath":           "deployment.yaml",
			"PatchPath":          "deployment_patch.yaml",
		})

		Expect(k8sClient.Create(ctx, localization)).To(Succeed())

		Eventually(Object(localization), "10s").Should(
			HaveField("Status.ArtifactRef.Name", Not(BeEmpty())))

		art := &artifactv1.Artifact{}
		art.Name = localization.Status.ArtifactRef.Name
		art.Namespace = localization.Namespace

		Eventually(Object(art), "5s").Should(HaveField("Spec.URL", Not(BeEmpty())))

		localized := strg.LocalPath(*art)
		Expect(localized).To(BeAnExistingFile())

		memFs := vfs.New(memoryfs.New())
		localizedArchiveData, err := os.OpenFile(localized, os.O_RDONLY, modeReadWriteUser)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(localizedArchiveData.Close()).To(Succeed())
		})
		Expect(tarutils.UnzipTarToFs(memFs, localizedArchiveData)).To(Succeed())
		Expect(memFs.Exists("deployment.yaml")).To(BeTrue())

		localizedData, err := memFs.ReadFile("deployment.yaml")
		Expect(err).ToNot(HaveOccurred())
		Expect(localizedData).To(MatchYAML(deploymentPatchResult))

	})
})

type options struct {
	basePath string
	filename string
	data     []byte
	path     string
}

func SetupMockResourceWithData(ctx context.Context,
	name, namespace string,
	repoName,
	componentName string,
	options options,
) *v1alpha1.Resource {
	res := &v1alpha1.Resource{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: v1alpha1.ResourceSpec{
			Resource: v1alpha1.ResourceID{Name: name},
			RepositoryRef: v1alpha1.ObjectKey{
				Namespace: namespace,
				Name:      repoName,
			},
			ComponentRef: v1alpha1.ObjectKey{
				Namespace: namespace,
				Name:      componentName,
			},
		},
	}
	Expect(k8sClient.Create(ctx, res)).To(Succeed())

	patchHelper := patch.NewSerialPatcher(res, k8sClient)

	path := options.path
	if options.data != nil {
		targetPath := filepath.Join(options.basePath, name)
		Expect(os.MkdirAll(targetPath, 0755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(targetPath, options.filename), options.data, 0644)).To(Succeed())
		path = targetPath
	}

	Expect(strg.ReconcileArtifact(
		ctx,
		res,
		name,
		path,
		fmt.Sprintf("%s.tar.gz", name),
		func(artifact *artifactv1.Artifact, s string) error {
			// Archive directory to storage
			if err := strg.Archive(artifact, path, nil); err != nil {
				return fmt.Errorf("unable to archive artifact to storage: %w", err)
			}
			res.Status.ArtifactRef = corev1.LocalObjectReference{
				Name: artifact.Name,
			}
			return nil
		}),
	).To(Succeed())

	art := &artifactv1.Artifact{}
	art.Name = res.Status.ArtifactRef.Name
	art.Namespace = res.Namespace
	Eventually(Object(art), "5s").Should(HaveField("Spec.URL", Not(BeEmpty())))

	Eventually(func(ctx context.Context) error {
		status.MarkReady(recorder, res, "applied mock resource")
		return status.UpdateStatus(ctx, patchHelper, res, recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return res
}

func SetupKustomizePatchLocalization(data map[string]string) *v1alpha1.Localization {
	localizationTemplate, err := template.New("localization").Parse(localizationTemplateKustomizePatch)
	Expect(err).ToNot(HaveOccurred())
	var ltpl bytes.Buffer
	Expect(localizationTemplate.ExecuteTemplate(&ltpl, "localization", data)).To(Succeed())
	localization := &v1alpha1.Localization{}
	serializer := serializer.NewCodecFactory(k8sClient.Scheme()).UniversalDeserializer()
	_, _, err = serializer.Decode(ltpl.Bytes(), nil, localization)
	Expect(err).To(Not(HaveOccurred()))
	return localization
}

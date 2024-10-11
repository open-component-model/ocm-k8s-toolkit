package kustomizepatch

import (
	"io"
	"os"
	"path/filepath"

	_ "embed"

	"github.com/mandelsoft/vfs/pkg/memoryfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"ocm.software/ocm/api/utils/tarutils"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	localizationTypes "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/types"
)

//go:embed testdata/deployment.yaml
var deploymentYAML []byte

//go:embed testdata/deployment_patch_strategic_merge.yaml
var deploymentPatchYAML []byte

//go:embed testdata/deployment_patch_strategic_merge_result.yaml
var deploymentPatchStrategicMergeResultYAML []byte

var _ = Describe("StrategicMerge", func() {

	It("should localize strategic merge patch_test", func(ctx SpecContext) {

		tmp := GinkgoT().TempDir()

		cls := localizationTypes.ResourceLocalizationSource{
			LocalizationSource: &localizationTypes.StaticLocalizationReference{
				Path: GenerateDeployPatch(tmp, deploymentPatchYAML),
			},
			Strategy: v1alpha1.LocalizationStrategy{
				KustomizePatch: &v1alpha1.LocalizationStrategyKustomizePatch{
					Path: "deployment.yaml",
					Patches: []v1alpha1.LocalizationKustomizePatch{
						{
							Path: "deployment_patch.yaml",
							Target: &v1alpha1.LocalizationSelector{
								LocalizationSelectorReference: v1alpha1.LocalizationSelectorReference{
									GroupVersionKind: v1.GroupVersionKind{
										Kind: "Deployment",
									},
								},
							},
						},
					},
				},
			},
		}

		clt := localizationTypes.StaticLocalizationReference{
			Path: GenerateDeploy(tmp, deploymentYAML),
		}

		target, err := Localize(ctx, &cls, &clt)
		Expect(err).ToNot(HaveOccurred())

		Expect(target).ToNot(BeEmpty())

		Expect(target).To(BeAnExistingFile())
		data, err := os.ReadFile(filepath.Join(target, "deployment.yaml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(MatchYAML(deploymentPatchStrategicMergeResultYAML))
	})

})

func GenerateDeployPatch(base string, data []byte) string {
	memVFS := vfs.New(memoryfs.New())
	Expect(memVFS.WriteFile("deployment_patch.yaml", data, os.ModePerm)).
		ToNot(HaveOccurred())

	srcPatches := filepath.Join(base, "source_patches")
	Expect(os.Mkdir(srcPatches, os.ModePerm)).To(Succeed())
	patchPath := filepath.Join(srcPatches, "patch_deploy.tar")
	Expect(tarutils.CreateTarFromFs(
		memVFS,
		patchPath,
		func(w io.Writer) io.WriteCloser {
			return tarutils.Gzip(w)
		},
	)).ToNot(HaveOccurred())

	return patchPath
}

func GenerateDeploy(base string, data []byte) string {
	memVFS := vfs.New(memoryfs.New())
	Expect(memVFS.WriteFile("deployment.yaml", data, os.ModePerm)).ToNot(HaveOccurred())

	targets := filepath.Join(base, "targets")
	Expect(os.Mkdir(targets, os.ModePerm)).To(Succeed())
	patchPath := filepath.Join(targets, "deploy.tar")
	Expect(tarutils.CreateTarFromFs(
		memVFS,
		patchPath,
		func(w io.Writer) io.WriteCloser {
			return tarutils.Gzip(w)
		},
	)).ToNot(HaveOccurred())

	return patchPath
}

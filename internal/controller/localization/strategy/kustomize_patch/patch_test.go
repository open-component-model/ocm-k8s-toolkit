package kustomize_patch

import (
	"io"
	"os"
	"path/filepath"

	"github.com/mandelsoft/vfs/pkg/memoryfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"ocm.software/ocm/api/utils/tarutils"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	types2 "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/types"
)

var _ = Describe("StrategicMerge", func() {

	It("should localize strategic merge patch_test", func(ctx SpecContext) {
		tmp := GinkgoT().TempDir()

		cls := types2.ComponentLocalizationSource{
			ComponentLocalizationReference: &types2.ComponentLocalizationReference{
				LocalArtifactPath: GenerateDeployPatch(tmp),
			},
			Strategy: v1alpha1.LocalizationStrategy{
				Type: v1alpha1.LocalizationStrategyTypeKustomizePatch,
				LocalizationStrategyKustomizePatch: &v1alpha1.LocalizationStrategyKustomizePatch{
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

		clt := types2.ComponentLocalizationReference{
			LocalArtifactPath: GenerateDeploy(tmp),
		}

		target, err := Localize(ctx, &cls, &clt)
		Expect(err).ToNot(HaveOccurred())

		Expect(target).ToNot(BeEmpty())
	})

})

func GenerateDeployPatch(base string) string {
	memVFS := vfs.New(memoryfs.New())
	Expect(memVFS.WriteFile("deployment_patch.yaml", []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-deployment
spec:
  replicas: 3
`), os.ModePerm)).ToNot(HaveOccurred())

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

func GenerateDeploy(base string) string {
	memVFS := vfs.New(memoryfs.New())
	Expect(memVFS.WriteFile("deployment.yaml", []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-deployment
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: my-container
        image: my-image
`), os.ModePerm)).ToNot(HaveOccurred())

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

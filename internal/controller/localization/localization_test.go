package localization

import (
	"bytes"
	"context"
	"fmt"
	"io"
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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	ocmbuilder "ocm.software/ocm/api/helper/builder"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/utils/tarutils"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	"github.com/mandelsoft/vfs/pkg/osfs"
	environment "ocm.software/ocm/api/helper/env"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

const modeReadWriteUser = 0o600

var (
	//go:embed strategy/mapped/testdata/descriptor-list.yaml
	descriptorListYAML []byte
	//go:embed strategy/mapped/testdata/replaced-values.yaml
	replacedValuesYAML []byte
	//go:embed strategy/mapped/testdata/replaced-deployment.yaml
	replacedDeploymentYAML []byte
	//go:embed strategy/mapped/testdata/localization-config.yaml
	configYAML []byte
	//go:embed testdata/localized_resource_patch.yaml.tmpl
	localizationTemplateKustomizePatch string
)

const (
	Namespace         = "test-namespace"
	RepositoryObj     = "test-repository"
	ComponentObj      = "test-component"
	SourceResourceObj = "source-test-util"
	TargetResourceObj = "target-test-util"
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

	It("should localize an artifact from a util based on a config supplied in a sibling util", func(ctx SpecContext) {
		component := SetupComponentWithDescriptorList(ctx,
			ComponentObj,
			Namespace,
			v1alpha1.ComponentInfo{
				Component:      "acme.org/test",
				Version:        "1.0.0",
				RepositorySpec: &apiextensionsv1.JSON{Raw: []byte(`{}`)},
			},
			tmp,
			descriptorListYAML,
		)
		DeferCleanup(func(ctx SpecContext) {
			Expect(k8sClient.Delete(ctx, component, client.PropagationPolicy(metav1.DeletePropagationForeground))).To(Succeed())
		})

		targetResource = SetupMockResourceWithData(ctx,
			TargetResourceObj,
			Namespace,
			options{
				basePath: tmp,
				dataPath: filepath.Join("strategy", "mapped", "testdata", "deployment-instruction-helm"),
				componentRef: v1alpha1.ObjectKey{
					Namespace: Namespace,
					Name:      ComponentObj,
				},
			},
		)
		DeferCleanup(func(ctx SpecContext) {
			Expect(k8sClient.Delete(ctx, targetResource, client.PropagationPolicy(metav1.DeletePropagationForeground))).To(Succeed())
		})
		sourceResource = SetupMockResourceWithData(ctx,
			SourceResourceObj,
			Namespace,
			options{
				basePath: tmp,
				data:     bytes.NewReader(configYAML),
				componentRef: v1alpha1.ObjectKey{
					Namespace: Namespace,
					Name:      ComponentObj,
				},
			},
		)
		DeferCleanup(func(ctx SpecContext) {
			Expect(k8sClient.Delete(ctx, sourceResource, client.PropagationPolicy(metav1.DeletePropagationForeground))).To(Succeed())
		})

		localization := SetupLocalizedResource(ctx, map[string]string{
			"Namespace":          Namespace,
			"Name":               Localization,
			"TargetResourceName": targetResource.Name,
			"SourceResourceName": sourceResource.Name,
		})
		DeferCleanup(func(ctx SpecContext) {
			Expect(k8sClient.Delete(ctx, localization, client.PropagationPolicy(metav1.DeletePropagationForeground))).To(Succeed())
		})

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

		valuesData, err := memFs.ReadFile("values.yaml")
		Expect(err).ToNot(HaveOccurred())
		Expect(valuesData).To(MatchYAML(replacedValuesYAML))

		deploymentData, err := memFs.ReadFile(filepath.Join("templates", "deployment.yaml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(deploymentData).To(BeEquivalentTo(replacedDeploymentYAML))

	})
})

type options struct {
	basePath string

	// option one to create a util: directly pass the data
	data io.Reader
	// option two to create a util: pass the path to the data
	dataPath string

	componentRef v1alpha1.ObjectKey
}

func SetupMockResourceWithData(ctx context.Context,
	name, namespace string,
	options options,
) *v1alpha1.Resource {
	res := &v1alpha1.Resource{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: v1alpha1.ResourceSpec{
			Resource: v1alpha1.ResourceID{
				ByReference: v1alpha1.ResourceReference{
					Resource: v1.NewIdentity(name),
				},
			},
			ComponentRef: corev1.LocalObjectReference{
				Name: options.componentRef.Name,
			},
		},
	}
	Expect(k8sClient.Create(ctx, res)).To(Succeed())

	patchHelper := patch.NewSerialPatcher(res, k8sClient)

	path := options.basePath

	Expect(strg.ReconcileArtifact(
		ctx,
		res,
		name,
		path,
		fmt.Sprintf("%s.tar.gz", name),
		func(artifact *artifactv1.Artifact, s string) error {
			// Archive directory to storage
			if options.data != nil {
				if err := strg.Copy(artifact, options.data); err != nil {
					return fmt.Errorf("unable to archive artifact to storage: %w", err)
				}
			}
			if options.dataPath != "" {
				abs, err := filepath.Abs(options.dataPath)
				if err != nil {
					return fmt.Errorf("unable to get absolute path: %w", err)
				}
				if err := strg.Archive(artifact, abs, nil); err != nil {
					return fmt.Errorf("unable to archive artifact to storage: %w", err)
				}
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
		status.MarkReady(recorder, res, "applied mock util")
		return status.UpdateStatus(ctx, patchHelper, res, recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return res
}

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

func SetupComponentWithDescriptorList(
	ctx context.Context,
	name, namespace string,
	info v1alpha1.ComponentInfo,
	basePath string,
	descriptorListData []byte,
) *v1alpha1.Component {
	dir := filepath.Join(basePath, "descriptor")
	Expect(os.Mkdir(dir, os.ModePerm|os.ModeDir)).To(Succeed())
	path := filepath.Join(dir, v1alpha1.OCMComponentDescriptorList)
	descriptorListWriter, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, os.ModePerm)
	Expect(err).ToNot(HaveOccurred())
	defer func() {
		Expect(descriptorListWriter.Close()).To(Succeed())
	}()
	_, err = descriptorListWriter.Write(descriptorListData)
	Expect(err).ToNot(HaveOccurred())

	component := &v1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ComponentSpec{
			RepositoryRef: v1alpha1.ObjectKey{Name: RepositoryObj, Namespace: namespace},
			Component:     info.Component,
		},
		Status: v1alpha1.ComponentStatus{
			ArtifactRef: corev1.LocalObjectReference{
				Name: name,
			},
			Component: info,
		},
	}
	Expect(k8sClient.Create(ctx, component)).To(Succeed())

	patchHelper := patch.NewSerialPatcher(component, k8sClient)

	Expect(strg.ReconcileArtifact(
		ctx,
		component,
		name,
		basePath,
		fmt.Sprintf("%s.tar.gz", name),
		func(artifact *artifactv1.Artifact, s string) error {
			if err := strg.Archive(artifact, dir, nil); err != nil {
				return fmt.Errorf("unable to archive artifact to storage: %w", err)
			}

			component.Status.ArtifactRef = corev1.LocalObjectReference{
				Name: artifact.Name,
			}
			component.Status.Component = info
			return nil
		}),
	).To(Succeed())

	art := &artifactv1.Artifact{}
	art.Name = component.Status.ArtifactRef.Name
	art.Namespace = component.Namespace
	Eventually(Object(art), "5s").Should(HaveField("Spec.URL", Not(BeEmpty())))

	Eventually(func(ctx context.Context) error {
		status.MarkReady(recorder, component, "applied mock component")
		return status.UpdateStatus(ctx, patchHelper, component, recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return component
}

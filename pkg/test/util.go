package test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/mandelsoft/vfs/pkg/memoryfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/utils/tarutils"
	"sigs.k8s.io/controller-runtime/pkg/client"

	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/ginkgo/v2"
	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/gomega"
	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

type MockResourceOptions struct {
	BasePath string

	// option one to create a resource: directly pass the Data
	Data io.Reader
	// option two to create a resource: pass the path to the Data
	DataPath string

	ComponentRef v1alpha1.ObjectKey

	Strg     *storage.Storage
	Clnt     client.Client
	Recorder record.EventRecorder
}

func SetupMockResourceWithData(
	ctx context.Context,
	name, namespace string,
	options *MockResourceOptions,
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
				Name: options.ComponentRef.Name,
			},
		},
	}
	Expect(options.Clnt.Create(ctx, res)).To(Succeed())

	patchHelper := patch.NewSerialPatcher(res, options.Clnt)

	path := options.BasePath

	err := options.Strg.ReconcileArtifact(
		ctx,
		res,
		name,
		path,
		fmt.Sprintf("%s.tar.gz", name),
		func(artifact *artifactv1.Artifact, _ string) error {
			// Archive directory to storage
			if options.Data != nil {
				if err := options.Strg.Copy(artifact, options.Data); err != nil {
					return fmt.Errorf("unable to archive artifact to storage: %w", err)
				}
			}
			if options.DataPath != "" {
				abs, err := filepath.Abs(options.DataPath)
				if err != nil {
					return fmt.Errorf("unable to get absolute path: %w", err)
				}
				if err := options.Strg.Archive(artifact, abs, nil); err != nil {
					return fmt.Errorf("unable to archive artifact to storage: %w", err)
				}
			}

			res.Status.ArtifactRef = corev1.LocalObjectReference{
				Name: artifact.Name,
			}

			return nil
		})
	Expect(err).ToNot(HaveOccurred())

	art := &artifactv1.Artifact{}
	art.Name = res.Status.ArtifactRef.Name
	art.Namespace = res.Namespace
	Eventually(Object(art), "5s").Should(HaveField("Spec.URL", Not(BeEmpty())))

	Eventually(func(ctx context.Context) error {
		status.MarkReady(options.Recorder, res, "applied mock resource")

		return status.UpdateStatus(ctx, patchHelper, res, options.Recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return res
}

type MockComponentOptions struct {
	BasePath   string
	Strg       *storage.Storage
	Client     client.Client
	Recorder   record.EventRecorder
	Info       v1alpha1.ComponentInfo
	Repository string
}

func SetupComponentWithDescriptorList(
	ctx context.Context,
	name, namespace string,
	descriptorListData []byte,
	options *MockComponentOptions,
) *v1alpha1.Component {
	dir := filepath.Join(options.BasePath, "descriptor")
	CreateTGZ(dir, map[string][]byte{
		v1alpha1.OCMComponentDescriptorList: descriptorListData,
	})
	component := &v1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ComponentSpec{
			RepositoryRef: v1alpha1.ObjectKey{Name: options.Repository, Namespace: namespace},
			Component:     options.Info.Component,
		},
		Status: v1alpha1.ComponentStatus{
			ArtifactRef: corev1.LocalObjectReference{
				Name: name,
			},
			Component: options.Info,
		},
	}
	Expect(options.Client.Create(ctx, component)).To(Succeed())

	patchHelper := patch.NewSerialPatcher(component, options.Client)

	Expect(options.Strg.ReconcileArtifact(
		ctx,
		component,
		name,
		options.BasePath,
		fmt.Sprintf("%s.tar.gz", name),
		func(artifact *artifactv1.Artifact, _ string) error {
			if err := options.Strg.Archive(artifact, dir, nil); err != nil {
				return fmt.Errorf("unable to archive artifact to storage: %w", err)
			}

			component.Status.ArtifactRef = corev1.LocalObjectReference{
				Name: artifact.Name,
			}
			component.Status.Component = options.Info

			return nil
		}),
	).To(Succeed())

	art := &artifactv1.Artifact{}
	art.Name = component.Status.ArtifactRef.Name
	art.Namespace = component.Namespace
	Eventually(Object(art), "5s").Should(HaveField("Spec.URL", Not(BeEmpty())))

	Eventually(func(ctx context.Context) error {
		status.MarkReady(options.Recorder, component, "applied mock component")

		return status.UpdateStatus(ctx, patchHelper, component, options.Recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return component
}

func VerifyArtifact(strg *storage.Storage, art *artifactv1.Artifact, files map[string]func(data []byte)) {
	GinkgoHelper()

	art = art.DeepCopy()

	Eventually(Object(art), "5s").Should(HaveField("Spec.URL", Not(BeEmpty())))

	localized := strg.LocalPath(art)
	Expect(localized).To(BeAnExistingFile())

	memFs := vfs.New(memoryfs.New())
	localizedArchiveData, err := os.OpenFile(localized, os.O_RDONLY, 0o600)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		Expect(localizedArchiveData.Close()).To(Succeed())
	})
	Expect(tarutils.UnzipTarToFs(memFs, localizedArchiveData)).To(Succeed())

	for fileName, assert := range files {
		data, err := memFs.ReadFile(fileName)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("expected %s to be present and be readable", fileName))
		assert(data)
	}
}

func CreateTGZ(tgzPackageDir string, data map[string][]byte) {
	GinkgoHelper()
	Expect(os.Mkdir(tgzPackageDir, os.ModePerm|os.ModeDir)).To(Succeed())
	for path, data := range data {
		path = filepath.Join(tgzPackageDir, path)
		writer, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, os.ModePerm)
		Expect(err).ToNot(HaveOccurred())
		defer func() {
			Expect(writer.Close()).To(Succeed())
		}()
		_, err = writer.Write(data)
		Expect(err).ToNot(HaveOccurred())
	}
}

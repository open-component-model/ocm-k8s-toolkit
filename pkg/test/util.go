package test

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/mandelsoft/goutils/testutils"
	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/ginkgo/v2"
	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/gomega"

	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/mandelsoft/vfs/pkg/memoryfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	"github.com/opencontainers/go-digest"
	"k8s.io/client-go/tools/record"
	"ocm.software/ocm/api/utils/tarutils"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	snapshotRegistry "github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

type MockResourceOptions struct {
	BasePath string

	// option one to create a resource: directly pass the Data
	Data io.Reader
	// option two to create a resource: pass the path to the Data
	DataPath string

	ComponentRef v1alpha1.ObjectKey

	Registry snapshotRegistry.RegistryType
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

	var data []byte
	var err error

	if options.Data != nil {
		data, err = io.ReadAll(options.Data)
		Expect(err).ToNot(HaveOccurred())
	}

	if options.DataPath != "" {
		f, err := os.Stat(options.DataPath)
		Expect(err).ToNot(HaveOccurred())

		// If the file is a directory, it must be tarred
		if f.IsDir() {
			tmpFile, err := os.CreateTemp("", "")
			defer func() {
				Expect(tmpFile.Close()).To(Succeed())
			}()
			Expect(err).ToNot(HaveOccurred())

			err = CreateTGZFromPath(options.DataPath, tmpFile.Name())
			Expect(err).ToNot(HaveOccurred())

			data, err = os.ReadFile(tmpFile.Name())
			Expect(err).ToNot(HaveOccurred())
		} else {
			data, err = os.ReadFile(options.DataPath)
			Expect(err).ToNot(HaveOccurred())
		}
	}

	// TODO: Check what about version?!
	version := "1.0.0"
	repositoryName := Must(snapshotRegistry.CreateRepositoryName(options.ComponentRef.Name, name))
	repository := Must(options.Registry.NewRepository(ctx, repositoryName))

	manifestDigest := Must(repository.PushSnapshot(ctx, version, data))
	snapshotCR := snapshotRegistry.Create(res, repositoryName, manifestDigest.String(), version, digest.FromBytes(data).String(), int64(len(data)))

	_ = Must(controllerutil.CreateOrUpdate(ctx, options.Clnt, &snapshotCR, func() error {
		if snapshotCR.ObjectMeta.CreationTimestamp.IsZero() {
			if err := controllerutil.SetControllerReference(res, &snapshotCR, options.Clnt.Scheme()); err != nil {
				return fmt.Errorf("failed to set controller reference: %w", err)
			}
		}

		res.Status.SnapshotRef = corev1.LocalObjectReference{
			Name: snapshotCR.GetName(),
		}

		return nil
	}))

	Eventually(func(ctx context.Context) error {
		status.MarkReady(options.Recorder, res, "applied mock resource")

		return status.UpdateStatus(ctx, patchHelper, res, options.Recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return res
}

type MockComponentOptions struct {
	BasePath   string
	Registry   snapshotRegistry.RegistryType
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
	CreateTGZFromData(dir, map[string][]byte{
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
			SnapshotRef: corev1.LocalObjectReference{
				Name: name,
			},
			Component: options.Info,
		},
	}
	Expect(options.Client.Create(ctx, component)).To(Succeed())

	patchHelper := patch.NewSerialPatcher(component, options.Client)

	data, err := os.ReadFile(filepath.Join(dir, v1alpha1.OCMComponentDescriptorList))
	Expect(err).ToNot(HaveOccurred())

	// TODO: Clean-up
	// Prevent error on NoOp for OCI push
	if len(data) == 0 {
		data = []byte("empty")
	}

	repositoryName := Must(snapshotRegistry.CreateRepositoryName(options.Repository, name))
	repository := Must(options.Registry.NewRepository(ctx, repositoryName))

	manifestDigest := Must(repository.PushSnapshot(ctx, options.Info.Version, data))
	snapshotCR := snapshotRegistry.Create(component, repositoryName, manifestDigest.String(), options.Info.Version, digest.FromBytes(data).String(), int64(len(data)))

	_ = Must(controllerutil.CreateOrUpdate(ctx, options.Client, &snapshotCR, func() error {
		if snapshotCR.ObjectMeta.CreationTimestamp.IsZero() {
			if err := controllerutil.SetControllerReference(component, &snapshotCR, options.Client.Scheme()); err != nil {
				return fmt.Errorf("failed to set controller reference: %w", err)
			}
		}

		component.Status.SnapshotRef = corev1.LocalObjectReference{
			Name: snapshotCR.GetName(),
		}

		component.Status.Component = options.Info

		return nil
	}))

	Eventually(func(ctx context.Context) error {
		status.MarkReady(options.Recorder, component, "applied mock component")

		return status.UpdateStatus(ctx, patchHelper, component, options.Recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return component
}

func VerifyArtifact(ctx context.Context, registry snapshotRegistry.RegistryType, snapshotCR *v1alpha1.Snapshot, files map[string]func(data []byte)) {
	GinkgoHelper()

	repository := Must(registry.NewRepository(ctx, snapshotCR.Spec.Repository))

	data := Must(repository.FetchSnapshot(ctx, snapshotCR.GetDigest()))

	memFs := vfs.New(memoryfs.New())
	Expect(tarutils.UnzipTarToFs(memFs, data)).To(Succeed())

	for fileName, assert := range files {
		data, err := memFs.ReadFile(fileName)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("expected %s to be present and be readable", fileName))
		assert(data)
	}
}

func CreateTGZFromData(tgzPackageDir string, data map[string][]byte) {
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

func CreateTGZFromPath(srcDir, tarPath string) error {
	GinkgoHelper()
	// Create the output tar file
	tarFile, err := os.Create(tarPath)
	Expect(err).ToNot(HaveOccurred())
	defer func() {
		Expect(tarFile.Close()).To(Succeed())
	}()

	// Create a new tar writer
	tarWriter := tar.NewWriter(tarFile)
	defer func() {
		Expect(tarWriter.Close()).To(Succeed())
	}()

	// Walk through the source directory
	return filepath.Walk(srcDir, func(file string, fileInfo os.FileInfo, err error) error {
		Expect(err).ToNot(HaveOccurred())

		if !fileInfo.Mode().IsRegular() {
			return nil
		}

		if fileInfo.IsDir() {
			return nil
		}

		// Create tar header
		header, err := tar.FileInfoHeader(fileInfo, fileInfo.Name())
		Expect(err).ToNot(HaveOccurred())

		// Use relative path for header.Name to preserve folder structure
		relPath, err := filepath.Rel(srcDir, file)
		Expect(err).ToNot(HaveOccurred())
		header.Name = relPath

		// Write header
		err = tarWriter.WriteHeader(header)
		Expect(err).ToNot(HaveOccurred())

		// Open the file
		f, err := os.Open(file)
		Expect(err).ToNot(HaveOccurred())
		defer func() {
			Expect(f.Close()).To(Succeed())
		}()

		// Copy file data into the tar archive
		_, err = io.Copy(tarWriter, f)
		Expect(err).ToNot(HaveOccurred())

		return nil
	})
}

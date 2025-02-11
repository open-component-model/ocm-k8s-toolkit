package test

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/ginkgo/v2"
	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/gomega"

	"github.com/mandelsoft/vfs/pkg/memoryfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	"ocm.software/ocm/api/utils/tarutils"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	snapshotRegistry "github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
)

func VerifyArtifact(ctx context.Context, registry snapshotRegistry.RegistryType, snapshotCR *v1alpha1.Snapshot, files map[string]func(data []byte)) {
	GinkgoHelper()

	repository, err := registry.NewRepository(ctx, snapshotCR.Spec.Repository)
	Expect(err).ToNot(HaveOccurred())

	data, err := repository.FetchSnapshot(ctx, snapshotCR.GetDigest())
	Expect(err).ToNot(HaveOccurred())

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

func CreateTGZFromPath(srcDir, tarPath string) (err error) {
	// Create the output tar file
	tarFile, err := os.Create(tarPath)
	if err != nil {
		return fmt.Errorf("could not create file: %w", err)
	}
	defer func() {
		err = tarFile.Close()
	}()

	// Create a new tar writer
	tarWriter := tar.NewWriter(tarFile)
	defer func() {
		err = tarWriter.Close()
	}()

	// Walk through the source directory
	return filepath.Walk(srcDir, func(file string, fileInfo os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("could not read file %s: %w", file, err)
		}

		if !fileInfo.Mode().IsRegular() {
			return nil
		}

		if fileInfo.IsDir() {
			return nil
		}

		// Create tar header
		header, err := tar.FileInfoHeader(fileInfo, fileInfo.Name())
		if err != nil {
			return fmt.Errorf("could not create tar header: %w", err)
		}

		// Use relative path for header.Name to preserve folder structure
		relPath, err := filepath.Rel(srcDir, file)
		if err != nil {
			return fmt.Errorf("could not create relative path: %w", err)
		}
		header.Name = relPath

		// Write header
		err = tarWriter.WriteHeader(header)
		if err != nil {
			return fmt.Errorf("could not write tar header: %w", err)
		}

		// Open the file
		f, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("could not open file %s: %w", file, err)
		}
		defer func() {
			err = f.Close()
		}()

		// Copy file data into the tar archive
		_, err = io.Copy(tarWriter, f)
		if err != nil {
			return fmt.Errorf("could not copy file %s: %w", file, err)
		}

		return nil
	})
}

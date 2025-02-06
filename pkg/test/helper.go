package test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	"ocm.software/ocm/api/utils/tarutils"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"

	ociV1 "github.com/opencontainers/image-spec/specs-go/v1"

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

func CreateLocalOCIArtifact(ctx context.Context, dir string, blob, config []byte) error {
	store, err := oci.New(dir)
	if err != nil {
		return fmt.Errorf("error creating OCI layout: %w", err)
	}

	// Prepare and upload blob
	blobDescriptor := ociV1.Descriptor{
		MediaType: ociV1.MediaTypeImageLayer,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}

	if err := store.Push(ctx, blobDescriptor, content.NewVerifyReader(
		bytes.NewReader(blob),
		blobDescriptor,
	)); err != nil {
		return fmt.Errorf("error pushing blob: %w", err)
	}

	// Prepare and upload image config
	imageConfigDescriptor := ociV1.Descriptor{
		MediaType: ociV1.MediaTypeImageConfig,
		Digest:    digest.FromBytes(config),
		Size:      int64(len(config)),
	}

	if err := store.Push(ctx, imageConfigDescriptor, content.NewVerifyReader(
		bytes.NewReader(config),
		imageConfigDescriptor,
	)); err != nil {
		return fmt.Errorf("error pushing config: %w", err)
	}

	// Prepare and upload manifest
	manifest := ociV1.Manifest{
		Versioned: specs.Versioned{SchemaVersion: v1alpha1.OCISchemaVersion},
		MediaType: ociV1.MediaTypeImageManifest,
		Config:    imageConfigDescriptor,
		Layers:    []ociV1.Descriptor{blobDescriptor},
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("oci: error marshaling manifest: %w", err)
	}

	manifestDigest := digest.FromBytes(manifestBytes)

	manifestDescriptor := ociV1.Descriptor{
		MediaType: manifest.MediaType,
		Digest:    manifestDigest,
		Size:      int64(len(manifestBytes)),
	}

	if err := store.Push(ctx, manifestDescriptor, content.NewVerifyReader(
		bytes.NewReader(manifestBytes),
		manifestDescriptor,
	)); err != nil {
		return fmt.Errorf("oci: error pushing manifest: %w", err)
	}

	return nil
}

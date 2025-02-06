package test

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/gomega"

	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/opencontainers/go-digest"
	"k8s.io/client-go/tools/record"
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
	// TODO: Check removal as this should be the basePath of the removed artifact
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
	repositoryName, err := snapshotRegistry.CreateRepositoryName(options.ComponentRef.Name, name)
	Expect(err).ToNot(HaveOccurred())
	repository, err := options.Registry.NewRepository(ctx, repositoryName)
	Expect(err).ToNot(HaveOccurred())

	manifestDigest, err := repository.PushSnapshot(ctx, version, data)
	Expect(err).ToNot(HaveOccurred())
	snapshotCR := snapshotRegistry.Create(res, repositoryName, manifestDigest.String(), version, digest.FromBytes(data).String(), int64(len(data)))

	_, err = controllerutil.CreateOrUpdate(ctx, options.Clnt, &snapshotCR, func() error {
		if snapshotCR.ObjectMeta.CreationTimestamp.IsZero() {
			if err := controllerutil.SetControllerReference(res, &snapshotCR, options.Clnt.Scheme()); err != nil {
				return fmt.Errorf("failed to set controller reference: %w", err)
			}
		}

		res.Status.SnapshotRef = corev1.LocalObjectReference{
			Name: snapshotCR.GetName(),
		}

		return nil
	})
	Expect(err).ToNot(HaveOccurred())

	Eventually(func(ctx context.Context) error {
		status.MarkReady(options.Recorder, res, "applied mock resource")

		return status.UpdateStatus(ctx, patchHelper, res, options.Recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return res
}

package test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/ginkgo/v2"
	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/gomega"

	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/opencontainers/go-digest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	snapshotRegistry "github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

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
	// TODO: Find a better way........
	//  Do we need to write the file?
	dir, err := os.MkdirTemp("", "descriptor-list-*")
	descriptorDir := filepath.Join(dir, "descriptors")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() error {
		return os.RemoveAll(dir)
	})

	CreateTGZFromData(descriptorDir, map[string][]byte{
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
	}
	Expect(options.Client.Create(ctx, component)).To(Succeed())

	patchHelper := patch.NewSerialPatcher(component, options.Client)

	data, err := os.ReadFile(filepath.Join(descriptorDir, v1alpha1.OCMComponentDescriptorList))
	Expect(err).ToNot(HaveOccurred())

	// TODO: Clean-up
	// Prevent error on NoOp for OCI push
	if len(data) == 0 {
		data = []byte("empty")
	}

	repositoryName, err := snapshotRegistry.CreateRepositoryName(options.Repository, name)
	Expect(err).ToNot(HaveOccurred())

	repository, err := options.Registry.NewRepository(ctx, repositoryName)
	Expect(err).ToNot(HaveOccurred())

	manifestDigest, err := repository.PushSnapshot(ctx, options.Info.Version, data)
	Expect(err).ToNot(HaveOccurred())

	snapshotCR := snapshotRegistry.Create(component, repositoryName, manifestDigest.String(), options.Info.Version, digest.FromBytes(data).String(), int64(len(data)))

	_, err = controllerutil.CreateOrUpdate(ctx, options.Client, &snapshotCR, func() error {
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
	})
	Expect(err).ToNot(HaveOccurred())

	Eventually(func(ctx context.Context) error {
		status.MarkReady(options.Recorder, component, "applied mock component")

		return status.UpdateStatus(ctx, patchHelper, component, options.Recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return component
}

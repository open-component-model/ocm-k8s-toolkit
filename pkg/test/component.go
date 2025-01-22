package test

import (
	"context"
	"fmt"
	"time"

	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/gomega"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/opencontainers/go-digest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

type MockComponentOptions struct {
	Registry   snapshot.RegistryType
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

	repositoryName, err := snapshot.CreateRepositoryName(options.Repository, name)
	Expect(err).ToNot(HaveOccurred())

	repository, err := options.Registry.NewRepository(ctx, repositoryName)
	Expect(err).ToNot(HaveOccurred())

	manifestDigest, err := repository.PushSnapshot(ctx, options.Info.Version, descriptorListData)
	Expect(err).ToNot(HaveOccurred())

	snapshotCR := snapshot.Create(
		component,
		repositoryName,
		manifestDigest.String(),
		&v1alpha1.BlobInfo{
			Digest: digest.FromBytes(descriptorListData).String(),
			Tag:    options.Info.Version,
			Size:   int64(len(descriptorListData)),
		},
	)

	_, err = controllerutil.CreateOrUpdate(ctx, options.Client, snapshotCR, func() error {
		if snapshotCR.ObjectMeta.CreationTimestamp.IsZero() {
			if err := controllerutil.SetControllerReference(component, snapshotCR, options.Client.Scheme()); err != nil {
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

	// Marks snapshot as ready
	conditions.MarkTrue(snapshotCR, "Ready", "ready", "message")
	Expect(options.Client.Status().Update(ctx, snapshotCR)).To(Succeed())

	Eventually(func(ctx context.Context) error {
		status.MarkReady(options.Recorder, component, "applied mock component")

		return status.UpdateStatus(ctx, patchHelper, component, options.Recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return component
}

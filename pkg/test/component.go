package test

import (
	"context"
	"strings"
	"time"

	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/gomega"

	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/opencontainers/go-digest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

type MockComponentOptions struct {
	Registry   ociartifact.RegistryType
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

	repositoryName, err := ociartifact.CreateRepositoryName(component.Spec.Component)
	Expect(err).ToNot(HaveOccurred())

	repository, err := options.Registry.NewRepository(ctx, repositoryName)
	Expect(err).ToNot(HaveOccurred())

	manifestDigest, err := repository.PushArtifact(ctx, options.Info.Version, descriptorListData)
	Expect(err).ToNot(HaveOccurred())

	component.Status.OCIArtifact = &v1alpha1.OCIArtifactInfo{
		Repository: repositoryName,
		Digest:     manifestDigest.String(),
		Blob: &v1alpha1.BlobInfo{
			Digest: digest.FromBytes(descriptorListData).String(),
			Tag:    options.Info.Version,
			Size:   int64(len(descriptorListData)),
		},
	}

	component.Status.Component = options.Info

	Eventually(func(ctx context.Context) error {
		status.MarkReady(options.Recorder, component, "applied mock component")

		return status.UpdateStatus(ctx, patchHelper, component, options.Recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return component
}

func GenerateComponentName(testName string) string {
	replaced := strings.ToLower(strings.ReplaceAll(testName, " ", "-"))
	maxLength := 63 // RFC 1123 Label Names
	if len(replaced) > maxLength {
		return replaced[:maxLength]
	}

	return replaced
}

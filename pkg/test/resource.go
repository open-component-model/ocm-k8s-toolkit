package test

import (
	"context"
	"io"
	"time"

	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/gomega"

	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/opencontainers/go-digest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/compression"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

type MockResourceOptions struct {
	// option one to create a resource: directly pass the Data
	Data io.Reader
	// option two to create a resource: pass the path to the Data
	DataPath string

	ComponentRef v1alpha1.ObjectKey

	Registry *ociartifact.Registry
	Clnt     client.Client
	Recorder record.EventRecorder
}

func SetupMockResourceWithData(
	ctx context.Context,
	name, namespace string,
	options *MockResourceOptions,
) *v1alpha1.Resource {
	resource := &v1alpha1.Resource{
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
	Expect(options.Clnt.Create(ctx, resource)).To(Succeed())

	patchHelper := patch.NewSerialPatcher(resource, options.Clnt)

	var data []byte
	var err error

	if options.Data != nil {
		data, err = io.ReadAll(options.Data)
		Expect(err).ToNot(HaveOccurred())
	}

	if options.DataPath != "" {
		data, err = compression.CreateTGZFromPath(options.DataPath)
		Expect(err).ToNot(HaveOccurred())
	}

	// The resource controller takes the version/tag that is specified in the resource access metadata. Since we do not
	// have a version/tag in the mock resource, we use a dummy version/tag.
	version := "dummy"
	repositoryName, err := ociartifact.CreateRepositoryName(options.ComponentRef.Name, name)
	Expect(err).ToNot(HaveOccurred())
	repository, err := options.Registry.NewRepository(ctx, repositoryName)
	Expect(err).ToNot(HaveOccurred())

	manifestDigest, err := repository.PushArtifact(ctx, version, data)
	Expect(err).ToNot(HaveOccurred())

	resource.Status.OCIArtifact = &v1alpha1.OCIArtifactInfo{
		Repository: repositoryName,
		Digest:     manifestDigest.String(),
		Blob: v1alpha1.BlobInfo{
			Digest: digest.FromBytes(data).String(),
			Tag:    version,
			Size:   int64(len(data)),
		},
	}

	Eventually(func(ctx context.Context) error {
		status.MarkReady(options.Recorder, resource, "applied mock resource")

		return status.UpdateStatus(ctx, patchHelper, resource, options.Recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return resource
}

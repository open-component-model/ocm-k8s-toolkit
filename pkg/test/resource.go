package test

import (
	"context"
	"io"
	"time"

	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"github.com/fluxcd/pkg/runtime/patch"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

type MockResourceOptions struct {
	// option one to create a resource: directly pass the Data
	Data io.Reader
	// option two to create a resource: pass the path to the Data
	DataPath string

	ComponentRef v1alpha1.ObjectKey

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

	Eventually(func(ctx context.Context) error {
		status.MarkReady(options.Recorder, resource, "applied mock resource")

		return status.UpdateStatus(ctx, patchHelper, resource, options.Recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return resource
}

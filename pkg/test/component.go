package test

import (
	"context"
	"time"

	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/gomega"

	"github.com/fluxcd/pkg/runtime/patch"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

type MockComponentOptions struct {
	Client     client.Client
	Recorder   record.EventRecorder
	Info       v1alpha1.ComponentInfo
	Repository string
}

func SetupComponentWithDescriptorList(
	ctx context.Context,
	name, namespace string,
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

	component.Status.Component = options.Info

	Eventually(func(ctx context.Context) error {
		status.MarkReady(options.Recorder, component, "applied mock component")

		return status.UpdateStatus(ctx, patchHelper, component, options.Recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return component
}

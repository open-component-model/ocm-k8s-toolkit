package test

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"k8s.io/client-go/tools/record"

	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/gomega"
	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	"github.com/fluxcd/pkg/runtime/patch"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/storage"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

type Options struct {
	BasePath string

	// option one to create a resource: directly pass the Data
	Data io.Reader
	// option two to create a resource: pass the path to the Data
	DataPath string

	ComponentRef v1alpha1.ObjectKey

	Strg     *storage.Storage
	Clnt     client.Client
	Recorder record.EventRecorder
}

func SetupMockResourceWithData(
	ctx context.Context,
	name, namespace string,
	options *Options,
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

	path := options.BasePath

	err := options.Strg.ReconcileArtifact(
		ctx,
		res,
		name,
		path,
		fmt.Sprintf("%s.tar.gz", name),
		func(artifact *artifactv1.Artifact, _ string) error {
			// Archive directory to storage
			if options.Data != nil {
				if err := options.Strg.Copy(artifact, options.Data); err != nil {
					return fmt.Errorf("unable to archive artifact to storage: %w", err)
				}
			}
			if options.DataPath != "" {
				abs, err := filepath.Abs(options.DataPath)
				if err != nil {
					return fmt.Errorf("unable to get absolute path: %w", err)
				}
				if err := options.Strg.Archive(artifact, abs, nil); err != nil {
					return fmt.Errorf("unable to archive artifact to storage: %w", err)
				}
			}

			res.Status.ArtifactRef = corev1.LocalObjectReference{
				Name: artifact.Name,
			}

			return nil
		})
	Expect(err).ToNot(HaveOccurred())

	art := &artifactv1.Artifact{}
	art.Name = res.Status.ArtifactRef.Name
	art.Namespace = res.Namespace
	Eventually(Object(art), "5s").Should(HaveField("Spec.URL", Not(BeEmpty())))

	Eventually(func(ctx context.Context) error {
		status.MarkReady(options.Recorder, res, "applied mock resource")

		return status.UpdateStatus(ctx, patchHelper, res, options.Recorder, time.Hour, nil)
	}).WithContext(ctx).Should(Succeed())

	return res
}

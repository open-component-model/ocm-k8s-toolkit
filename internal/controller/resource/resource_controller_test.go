/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resource

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	. "github.com/mandelsoft/goutils/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opencontainers/go-digest"
	. "ocm.software/ocm/api/helper/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	"ocm.software/ocm/api/ocm/extensions/artifacttypes"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/utils/accessio"
	"ocm.software/ocm/api/utils/accessobj"
	"ocm.software/ocm/api/utils/mime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	environment "ocm.software/ocm/api/helper/env"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
)

const (
	CTFPath          = "ocm-k8s-ctfstore--*"
	Namespace        = "test-namespace"
	RepositoryObj    = "test-repository"
	Component        = "ocm.software/test-component"
	ComponentObj     = "test-component"
	ComponentVersion = "1.0.0"
	ResourceObj      = "test-resource"
	ResourceVersion  = "1.0.0"
	ResourceContent  = "some important content"
)

var _ = Describe("Resource Controller", func() {
	var (
		ctx     context.Context
		cancel  context.CancelFunc
		env     *Builder
		ctfPath string
	)
	BeforeEach(func() {
		ctfPath = Must(os.MkdirTemp("", CTFPath))
		DeferCleanup(func() error {
			return os.RemoveAll(ctfPath)
		})

		env = NewBuilder(environment.FileSystem(osfs.OsFs))
		DeferCleanup(env.Cleanup)

		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
	})

	Context("resource controller", func() {
		It("can reconcile a resource", func() {
			By("creating namespace object")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: Namespace,
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

			By("preparing a mock component")
			prepareComponent(ctx, env, ctfPath)

			By("creating a resource object")
			resource := &v1alpha1.Resource{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: Namespace,
					Name:      ResourceObj,
				},
				Spec: v1alpha1.ResourceSpec{
					ComponentRef: corev1.LocalObjectReference{
						Name: ComponentObj,
					},
					Resource: v1alpha1.ResourceID{
						ByReference: v1alpha1.ResourceReference{
							Resource: v1.NewIdentity(ResourceObj),
						},
					},
					Interval: metav1.Duration{Duration: time.Minute * 5},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func(ctx SpecContext) {
				Expect(k8sClient.Delete(ctx, resource, client.PropagationPolicy(metav1.DeletePropagationForeground))).To(Succeed())
			})

			By("checking that the resource has been reconciled successfully")
			Eventually(komega.Object(resource), "5m").Should(
				HaveField("Status.ObservedGeneration", Equal(int64(1))))
			Expect(resource).To(HaveField("Status.SnapshotRef.Name", Not(BeEmpty())))
			Expect(resource).To(HaveField("Status.Resource.Name", Equal(ResourceObj)))
			Expect(resource).To(HaveField("Status.Resource.Type", Equal(artifacttypes.PLAIN_TEXT)))
			Expect(resource).To(HaveField("Status.Resource.Version", Equal(ResourceVersion)))

			By("checking that the snapshot has been created successfully")
			snapshotResource := Must(snapshot.GetSnapshotForOwner(ctx, k8sClient, resource))

			By("checking that the snapshot contains the correct content")
			snapshotRepository := Must(registry.NewRepository(ctx, snapshotResource.Spec.Repository))
			snapshotResourceContentReader := Must(snapshotRepository.FetchSnapshot(ctx, snapshotResource.GetDigest()))
			snapshotResourceContent := Must(io.ReadAll(snapshotResourceContentReader))
			Expect(string(snapshotResourceContent)).To(Equal(ResourceContent))
		})
	})
})

// prepareComponent essentially mocks the behavior of the component reconciler to provider the necessary component and
// artifact for the resource controller.
func prepareComponent(ctx context.Context, env *Builder, ctfPath string) {
	By("creating ocm repositories with a component and resource")
	env.OCMCommonTransport(ctfPath, accessio.FormatDirectory, func() {
		env.Component(Component, func() {
			env.Version(ComponentVersion, func() {
				env.Resource(ResourceObj, ResourceVersion, artifacttypes.PLAIN_TEXT, v1.LocalRelation, func() {
					env.BlobData(mime.MIME_TEXT, []byte(ResourceContent))
				})
			})
		})
	})

	By("creating a component descriptor")
	tmpDirCd := Must(os.MkdirTemp("/tmp", "descriptors-"))
	DeferCleanup(func() error {
		return os.RemoveAll(tmpDirCd)
	})
	repo := Must(ctf.Open(env, accessobj.ACC_WRITABLE, ctfPath, vfs.FileMode(vfs.O_RDWR), env))
	cv := Must(repo.LookupComponentVersion(Component, ComponentVersion))
	cd := Must(ocm.ListComponentDescriptors(ctx, cv, repo))
	dataCds := Must(yaml.Marshal(cd))
	MustBeSuccessful(os.WriteFile(filepath.Join(tmpDirCd, v1alpha1.OCMComponentDescriptorList), dataCds, 0o655))

	By("creating a component object")
	component := &v1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: Namespace,
			Name:      ComponentObj,
		},
		Spec: v1alpha1.ComponentSpec{
			RepositoryRef: v1alpha1.ObjectKey{
				Namespace: Namespace,
				Name:      RepositoryObj,
			},
			Component: Component,
			Semver:    ComponentVersion,
			Interval:  metav1.Duration{Duration: time.Minute * 10},
		},
	}
	Expect(k8sClient.Create(ctx, component)).To(Succeed())

	By("creating an component snapshot")
	repositoryName := Must(snapshot.CreateRepositoryName(component.Spec.RepositoryRef.Name, component.GetName()))
	repository := Must(registry.NewRepository(ctx, repositoryName))

	manifestDigest := Must(repository.PushSnapshot(ctx, ComponentVersion, dataCds))
	snapshotCR := snapshot.Create(component, repositoryName, manifestDigest.String(), ComponentVersion, digest.FromBytes(dataCds).String(), int64(len(dataCds)))

	_ = Must(controllerutil.CreateOrUpdate(ctx, k8sClient, &snapshotCR, func() error {
		if snapshotCR.ObjectMeta.CreationTimestamp.IsZero() {
			if err := controllerutil.SetControllerReference(component, &snapshotCR, k8sClient.Scheme()); err != nil {
				return fmt.Errorf("failed to set controller reference: %w", err)
			}
		}
		return nil
	}))

	By("updating the component object with the respective status")
	baseComponent := component.DeepCopy()
	ready := *conditions.TrueCondition("Ready", "ready", "message")
	ready.LastTransitionTime = metav1.Time{Time: time.Now()}
	baseComponent.Status.Conditions = []metav1.Condition{ready}
	baseComponent.Status.SnapshotRef = corev1.LocalObjectReference{Name: snapshotCR.GetName()}
	spec := Must(ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfPath))
	specData := Must(spec.MarshalJSON())
	baseComponent.Status.Component = v1alpha1.ComponentInfo{
		RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
		Component:      Component,
		Version:        ComponentVersion,
	}
	Expect(k8sClient.Status().Update(ctx, baseComponent)).To(Succeed())
}

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
	"net/http"
	"os"
	"time"

	"github.com/containers/image/v5/pkg/compression"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/mandelsoft/filepath/pkg/filepath"
	. "github.com/mandelsoft/goutils/testutils"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	. "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/extensions/artifacttypes"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/utils/accessio"
	"ocm.software/ocm/api/utils/accessobj"
	"ocm.software/ocm/api/utils/mime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/yaml"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
)

const (
	CTFPath          = "ocm-k8s-ctfstore--*"
	Namespace        = "test-namespace"
	Component        = "ocm.software/test-component"
	ComponentObj     = "test-component"
	ComponentVersion = "1.0.0"
	ResourceObj      = "test-resource"
	ResourceVersion  = "1.0.0"
	ResourceContent  = "resource content"
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
		var (
			componentName string
			testNumber    int
			//componentObj  *v1alpha1.Component
		)

		BeforeEach(func() {
			By("mocking the component controller")
			componentName = fmt.Sprintf("%s-%d", ComponentObj, testNumber)
			mockComponentReconciler(ctx, env, ctfPath, componentName)
		})

		It("can reconcile a resource", func() {

			By("creating a resource object")
			resource := &v1alpha1.Resource{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: Namespace,
					Name:      ResourceObj,
				},
				Spec: v1alpha1.ResourceSpec{
					ComponentRef: corev1.LocalObjectReference{
						Name: componentName,
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
			Expect(resource).To(HaveField("Status.ArtifactRef.Name", Not(BeEmpty())))
			Expect(resource).To(HaveField("Status.Resource.Name", Equal(ResourceObj)))
			Expect(resource).To(HaveField("Status.Resource.Type", Equal(artifacttypes.PLAIN_TEXT)))
			Expect(resource).To(HaveField("Status.Resource.Version", Equal(ResourceVersion)))

			By("checking that the artifact has been created successfully")
			artifact := &artifactv1.Artifact{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: resource.Namespace,
					Name:      resource.Status.ArtifactRef.Name,
				},
			}
			Eventually(komega.Get(artifact)).Should(Succeed())

			By("checking that the artifact server provides the resource")
			r := Must(http.Get(artifact.Spec.URL))
			Expect(r).Should(HaveHTTPStatus(http.StatusOK))

			By("checking that the resource content is correct")
			reader, decompressed, err := compression.AutoDecompress(r.Body)
			Expect(decompressed).To(BeTrue())
			DeferCleanup(func() {
				Expect(reader.Close()).To(Succeed())
			})
			Expect(err).To(BeNil())
			resourceContent := Must(io.ReadAll(reader))

			Expect(string(resourceContent)).To(Equal(ResourceContent))
		})
	})
})

func mockComponentReconciler(ctx context.Context, env *Builder, ctfPath, name string) {
	// Create a ctf storing a component-version with a blob data as content
	env.OCMCommonTransport(ctfPath, accessio.FormatDirectory, func() {
		env.Component(Component, func() {
			env.Version(ComponentVersion, func() {
				env.Resource(ResourceObj, ResourceVersion, artifacttypes.PLAIN_TEXT, v1.LocalRelation, func() {
					env.BlobData(mime.MIME_TEXT, []byte(ResourceContent))
				})
			})
		})
	})

	// Creating component descriptor
	tmp := Must(os.MkdirTemp("/tmp", "descriptors-"))
	DeferCleanup(func() error {
		return os.RemoveAll(tmp)
	})

	repo := Must(ctf.Open(env, accessobj.ACC_WRITABLE, ctfPath, vfs.FileMode(vfs.O_RDWR), env))
	cv := Must(repo.LookupComponentVersion(Component, ComponentVersion))
	cd := Must(ocm.ListComponentDescriptors(ctx, cv, repo))
	dataCds := Must(yaml.Marshal(cd))
	MustBeSuccessful(os.WriteFile(filepath.Join(tmp, v1alpha1.OCMComponentDescriptorList), dataCds, 0o655))

	// Create a component object
	component := &v1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: Namespace,
			Name:      name,
		},
		Spec: v1alpha1.ComponentSpec{
			RepositoryRef: v1alpha1.ObjectKey{
				Namespace: "some-namespace",
				Name:      "some-name",
			},
			Component: Component,
			Semver:    ComponentVersion,
			Interval:  metav1.Duration{Duration: time.Minute * 10},
		},
	}
	Expect(k8sClient.Create(ctx, component)).To(Succeed())

	// Create a component artifact object
	var artifactName string
	Expect(globStorage.ReconcileArtifact(ctx, component, "revision", tmp, "component-descriptor.tar.gz",
		func(art *artifactv1.Artifact, _ string) error {
			// Archive directory to storage
			if err := globStorage.Archive(art, tmp, nil); err != nil {
				return fmt.Errorf("unable to archive artifact to storage: %w", err)
			}

			artifactName = art.Name

			return nil
		},
	)).To(Succeed())

	// Create a component artifact object
	artifact := &artifactv1.Artifact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      artifactName,
			Namespace: Namespace,
		},
	}
	Eventually(komega.Get(artifact)).Should(Succeed())

	// Update the component object status
	baseComponent := component.DeepCopy()
	ready := *conditions.TrueCondition("Ready", "ready", "message")
	ready.LastTransitionTime = metav1.Time{Time: time.Now()}
	baseComponent.Status.Conditions = []metav1.Condition{ready}
	baseComponent.Status.ArtifactRef = corev1.LocalObjectReference{Name: artifact.ObjectMeta.Name}
	spec := Must(ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfPath))
	specData := Must(spec.MarshalJSON())
	baseComponent.Status.Component = v1alpha1.ComponentInfo{
		RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
		Component:      Component,
		Version:        ComponentVersion,
	}
	Expect(k8sClient.Status().Update(ctx, baseComponent)).To(Succeed())
}

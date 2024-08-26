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

package controller

import (
	"context"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/tar"
	"github.com/mandelsoft/filepath/pkg/filepath"
	. "github.com/mandelsoft/goutils/testutils"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/vfs"
	. "github.com/onsi/gomega"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/ocm"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"net/http"
	. "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/utils/accessio"
	"ocm.software/ocm/api/utils/accessobj"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/yaml"
	"time"

	. "github.com/onsi/ginkgo/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

const (
	CTFPATH        = "ocm-k8s-utils--*"
	NAMESPACE      = "test-namespace"
	REPOSITORY_OBJ = "test-repository"
	COMPONENT      = "ocm.software/test-component"
	COMPONENT_OBJ  = "test-component"
	VERSION1       = "1.0.0"
)

var _ = Describe("Component Controller", func() {
	var (
		ctx     context.Context
		cancel  context.CancelFunc
		env     *Builder
		ctfpath string
	)
	BeforeEach(func() {
		ctfpath = Must(os.MkdirTemp("", CTFPATH))
		DeferCleanup(func() error {
			return os.RemoveAll(ctfpath)
		})
		env = NewBuilder(environment.FileSystem(osfs.OsFs))
		DeferCleanup(env.Cleanup)

		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
	})

	Context("component controller", func() {
		It("reconcile a component", func() {
			By("creating ocm repository with components")
			env.OCMCommonTransport(ctfpath, accessio.FormatDirectory, func() {
				env.Component(COMPONENT, func() {
					env.Version(VERSION1)
				})
			})

			By("creating namespace object")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: NAMESPACE,
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

			By("creating a repository object")
			spec := Must(ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfpath))
			specdata := Must(spec.MarshalJSON())
			repository := &deliveryv1alpha1.OCMRepository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: NAMESPACE,
					Name:      REPOSITORY_OBJ,
				},
				Spec: deliveryv1alpha1.OCMRepositorySpec{
					RepositorySpec: &apiextensionsv1.JSON{
						Raw: specdata,
					},
					Interval: metav1.Duration{Duration: time.Minute * 10},
				},
			}
			Expect(k8sClient.Create(ctx, repository)).To(Succeed())
			baseRepo := repository.DeepCopy()
			ready := *conditions.TrueCondition("Ready", "ready", "message")
			ready.LastTransitionTime = metav1.Time{Time: time.Now()}
			baseRepo.Status.Conditions = []metav1.Condition{ready}
			Expect(k8sClient.Status().Update(ctx, baseRepo)).To(Succeed())

			By("creating a component object")
			component := &deliveryv1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: NAMESPACE,
					Name:      COMPONENT_OBJ,
				},
				Spec: deliveryv1alpha1.ComponentSpec{
					RepositoryRef: deliveryv1alpha1.ObjectKey{
						Namespace: NAMESPACE,
						Name:      REPOSITORY_OBJ,
					},
					Component:              COMPONENT,
					EnforceDowngradability: false,
					Semver:                 "1.0.0",
					Interval:               metav1.Duration{Duration: time.Minute * 10},
				},
				Status: deliveryv1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())

			By("check that artifact has been created successfully")
			Eventually(komega.Object(component), "5m").Should(
				HaveField("Status.ArtifactRef.Name", Not(BeEmpty())))
			artifact := &artifactv1.Artifact{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: component.Namespace,
					Name:      component.Status.ArtifactRef.Name,
				},
			}
			Eventually(komega.Get(artifact)).Should(Succeed())

			By("check if the component descriptor list can be retrieved from the artifact server")
			r := Must(http.Get(artifact.Spec.URL))

			tmpdir := Must(os.MkdirTemp("/tmp", "descriptors-"))
			DeferCleanup(func() error {
				return os.RemoveAll(tmpdir)
			})
			MustBeSuccessful(tar.Untar(r.Body, tmpdir))

			repo := Must(ctf.Open(env, accessobj.ACC_WRITABLE, ctfpath, vfs.FileMode(vfs.O_RDWR), env))
			cv := Must(repo.LookupComponentVersion(COMPONENT, VERSION1))
			expecteddescs := &Components{List: Must(ocm.ListComponentDescriptors(cv, repo))}

			data := Must(os.ReadFile(filepath.Join(tmpdir, "component-descriptor.yaml")))
			descs := &Components{}
			MustBeSuccessful(yaml.Unmarshal(data, descs))
			Expect(descs).To(YAMLEqual(expecteddescs))
		})
	})
})

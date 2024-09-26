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

package ocmrepository

import (
	"context"
	"os"
	"time"

	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/mandelsoft/goutils/testutils"
	"github.com/mandelsoft/vfs/pkg/osfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	. "ocm.software/ocm/api/helper/builder"
	environment "ocm.software/ocm/api/helper/env"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/utils/accessio"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

const (
	CTFPath       = "ocm-k8s-ctfstore--*"
	Namespace     = "test-namespace"
	RepositoryObj = "test-repository"
	Component     = "ocm.software/test-component"
	ComponentObj  = "test-component"
	Version1      = "1.0.0"
)

var _ = Describe("Component Controller", func() {
	var (
		ctx     context.Context
		cancel  context.CancelFunc
		env     *Builder
		ctfpath string
	)
	BeforeEach(func() {
		ctfpath = Must(os.MkdirTemp("", CTFPath))
		DeferCleanup(func() error {
			return os.RemoveAll(ctfpath)
		})
		env = NewBuilder(environment.FileSystem(osfs.OsFs))
		DeferCleanup(env.Cleanup)

		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
	})

	Context("repository controller", func() {
		It("reconciles a repository", func() {
			By("creating an ocm repository")
			By("creating namespace object")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: Namespace,
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

			By("creating a repository object")
			spec := Must(ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfpath))
			specdata := Must(spec.MarshalJSON())
			repository := &v1alpha1.OCMRepository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: Namespace,
					Name:      RepositoryObj,
				},
				Spec: v1alpha1.OCMRepositorySpec{
					RepositorySpec: &apiextensionsv1.JSON{
						Raw: specdata,
					},
					Interval: metav1.Duration{Duration: time.Minute * 10},
				},
			}
			Expect(k8sClient.Create(ctx, repository)).To(Succeed())

			By("checking if the repository is ready")

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: Namespace, Name: RepositoryObj}, repository)).To(Succeed())
				return conditions.IsReady(repository)
			}).WithTimeout(5 * time.Second).Should(BeTrue())

			By("creating a component that uses this repository")
			env.OCMCommonTransport(ctfpath, accessio.FormatDirectory, func() {
				env.Component(Component, func() {
					env.Version(Version1)
				})
			})
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
					Component:              Component,
					EnforceDowngradability: false,
					Semver:                 "1.0.0",
					Interval:               metav1.Duration{Duration: time.Minute * 10},
				},
				Status: v1alpha1.ComponentStatus{},
			}
			Expect(k8sClient.Create(ctx, component)).To(Succeed())
			By("deleting the repository should not allow the deletion unless the component is removed")
			Expect(k8sClient.Delete(ctx, repository)).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: Namespace, Name: RepositoryObj}, repository)).To(Succeed())

			By("removing the component")
			Expect(k8sClient.Delete(ctx, component)).To(Succeed())

			By("checking if the repository is eventually deleted")
			Eventually(k8sClient.Get(ctx, types.NamespacedName{Namespace: Namespace, Name: RepositoryObj}, repository)).WithTimeout(10 * time.Second).ShouldNot(Succeed())

			tmpdir := Must(os.MkdirTemp("/tmp", "descriptors-"))
			DeferCleanup(func() error {
				return os.RemoveAll(tmpdir)
			})

		})
	})
})

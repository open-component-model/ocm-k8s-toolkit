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
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/projectionfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	. "ocm.software/ocm/api/helper/builder"
	ocmmetav1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/git"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/github"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/helm"
	ocmociartifact "ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
	"ocm.software/ocm/api/ocm/extensions/artifacttypes"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/utils/accessio"
	"ocm.software/ocm/api/utils/mime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	environment "ocm.software/ocm/api/helper/env"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/status"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/test"
)

var _ = Describe("Resource Controller", func() {
	var (
		env     *Builder
		tempDir string
	)

	BeforeEach(func() {
		tempDir = GinkgoT().TempDir()
		fs, err := projectionfs.New(osfs.OsFs, tempDir)
		Expect(err).NotTo(HaveOccurred())
		env = NewBuilder(environment.FileSystem(fs))
	})
	AfterEach(func() {
		Expect(env.Cleanup()).To(Succeed())
	})

	Context("resource controller", func() {
		var componentObj *v1alpha1.Component
		var namespace *corev1.Namespace
		var componentName, componentObjName, resourceName string
		var componentVersion string
		repositoryName := "ocm.software/test-repository"

		BeforeEach(func(ctx SpecContext) {
			componentObjName = test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			componentName = "ocm.software/test-component-" + test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			resourceName = "test-resource-" + test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			componentVersion = "v1.0.0"

			namespaceName := test.SanitizeNameForK8s(ctx.SpecReport().LeafNodeText)
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		})

		AfterEach(func(ctx SpecContext) {
			By("deleting the component")
			Expect(k8sClient.Delete(ctx, componentObj)).To(Succeed())
			Eventually(func(ctx context.Context) error {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(componentObj), componentObj)
				if err != nil {
					if errors.IsNotFound(err) {
						return nil
					}
					return err
				}

				return fmt.Errorf("resource %s still exists", componentObj.Name)
			}).WithContext(ctx).Should(Succeed())

			resources := &v1alpha1.ResourceList{}
			Expect(k8sClient.List(ctx, resources, client.InNamespace(namespace.GetName()))).To(Succeed())
			Expect(resources.Items).To(HaveLen(0))
		})

		DescribeTable("should reconcile a created resource",
			func(createCTF func() string, expSourceRef *v1alpha1.SourceReference) {
				By("creating a CTF")
				ctfPath := createCTF()

				spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfPath)
				Expect(err).NotTo(HaveOccurred())
				specData, err := spec.MarshalJSON()
				Expect(err).NotTo(HaveOccurred())

				By("mocking a component")
				componentObj = test.MockComponent(
					ctx,
					componentObjName,
					namespace.GetName(),
					&test.MockComponentOptions{
						Client:   k8sClient,
						Recorder: recorder,
						Info: v1alpha1.ComponentInfo{
							Component:      componentName,
							Version:        componentVersion,
							RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
						},
						Repository: repositoryName,
					},
				)

				By("creating a resource")
				resourceObj := &v1alpha1.Resource{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: namespace.GetName(),
					},
					Spec: v1alpha1.ResourceSpec{
						ComponentRef: corev1.LocalObjectReference{
							Name: componentObj.GetName(),
						},
						Resource: v1alpha1.ResourceID{
							ByReference: v1alpha1.ResourceReference{
								Resource: ocmmetav1.NewIdentity(resourceName),
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resourceObj)).To(Succeed())

				By("checking that the resource has been reconciled successfully")
				waitUntilResourceIsReady(ctx, resourceObj)

				if expSourceRef != nil {
					Expect(resourceObj.Status.Reference).To(Equal(expSourceRef))
				}

				By("deleting the resource")
				deleteResource(ctx, resourceObj)
			},

			Entry("plain text", func() string {
				ctfName := "plainText"
				env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(componentVersion, func() {
							env.Resource(resourceName, "1.0.0", artifacttypes.PLAIN_TEXT, ocmmetav1.LocalRelation, func() {
								env.BlobData(mime.MIME_TEXT, []byte("Hello World!"))
							})
						})
					})
				})
				return filepath.Join(tempDir, ctfName)
			},
				nil),
			Entry("OCI artifact access", func() string {
				ctfName := "ociArtifactAccess"
				env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(componentVersion, func() {
							env.Resource(resourceName, "1.0.0", artifacttypes.OCI_ARTIFACT, ocmmetav1.ExternalRelation, func() {
								env.Access(ocmociartifact.New("ghcr.io/open-component-model/ocm/ocm.software/ocmcli/ocmcli-image:0.24.0"))
							})
						})
					})
				})
				return filepath.Join(tempDir, ctfName)
			},
				&v1alpha1.SourceReference{
					Registry:   "ghcr.io",
					Repository: "open-component-model/ocm/ocm.software/ocmcli/ocmcli-image",
					Tag:        "0.24.0",
				},
			),
			Entry("Helm access", func() string {
				ctfName := "helmAccess"
				env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(componentVersion, func() {
							env.Resource(resourceName, "1.0.0", artifacttypes.HELM_CHART, ocmmetav1.ExternalRelation, func() {
								env.Access(helm.New("podinfo:6.7.1", "oci://ghcr.io/stefanprodan/charts"))
							})
						})
					})
				})
				return filepath.Join(tempDir, ctfName)
			},
				&v1alpha1.SourceReference{
					Registry:   "oci://ghcr.io/stefanprodan/charts",
					Repository: "podinfo",
					Reference:  "6.7.1",
				},
			),
			Entry("GitHub access", func() string {
				ctfName := "gitHubAccess"
				env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(componentVersion, func() {
							env.Resource(resourceName, "1.0.0", artifacttypes.DIRECTORY_TREE, ocmmetav1.ExternalRelation, func() {
								env.Access(github.New(
									"https://github.com/open-component-model/ocm-k8s-toolkit",
									"/repos/open-component-model/ocm-k8s-toolkit",
									"8f7e04f4b58d2a9e22f88e79dddfc36183688f28",
								))
							})
						})
					})
				})
				return filepath.Join(tempDir, ctfName)
			},
				&v1alpha1.SourceReference{
					Registry:   "https://github.com",
					Repository: "/open-component-model/ocm-k8s-toolkit",
					Reference:  "8f7e04f4b58d2a9e22f88e79dddfc36183688f28",
				},
			),
			Entry("git access", func() string {
				ctfName := "gitAccess"
				env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
					env.Component(componentName, func() {
						env.Version(componentVersion, func() {
							env.Resource(resourceName, "1.0.0", artifacttypes.DIRECTORY_TREE, ocmmetav1.ExternalRelation, func() {
								env.Access(git.New(
									"https://github.com/open-component-model/ocm-k8s-toolkit",
									git.WithRef("refs/heads/main"),
								))
							})
						})
					})
				})
				return filepath.Join(tempDir, ctfName)
			},
				&v1alpha1.SourceReference{
					Registry:   "https://github.com",
					Repository: "/open-component-model/ocm-k8s-toolkit",
					Reference:  "refs/heads/main",
				},
			),
		)

		It("should not reconcile when the component is not ready", func(ctx SpecContext) {
			By("mocking a component")
			componentObj = test.MockComponent(
				ctx,
				componentObjName,
				namespace.GetName(),
				&test.MockComponentOptions{
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      componentName,
						Version:        componentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: []byte("{}")},
					},
					Repository: repositoryName,
				},
			)

			By("marking the mocked component as not ready")
			componentObjNotReady := &v1alpha1.Component{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(componentObj), componentObjNotReady)).To(Succeed())

			status.MarkNotReady(recorder, componentObjNotReady, v1alpha1.ResourceIsNotAvailable, "mock component is not ready")
			Expect(k8sClient.Status().Update(ctx, componentObjNotReady)).To(Succeed())

			By("creating a resource")
			resourceObj := &v1alpha1.Resource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace.GetName(),
				},
				Spec: v1alpha1.ResourceSpec{
					ComponentRef: corev1.LocalObjectReference{
						Name: componentObj.GetName(),
					},
					Resource: v1alpha1.ResourceID{
						ByReference: v1alpha1.ResourceReference{
							Resource: ocmmetav1.NewIdentity(resourceName),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resourceObj)).To(Succeed())

			By("checking that the resource has not been reconciled successfully")
			resourceObjNotReady := &v1alpha1.Resource{}
			Eventually(func(g Gomega, ctx context.Context) error {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resourceObj), resourceObjNotReady)
				if err != nil {
					return fmt.Errorf("failed to get resource: %w", err)
				}

				if conditions.IsReady(resourceObjNotReady) {
					return fmt.Errorf("resource %s is ready", resourceObjNotReady.Name)
				}

				reason := conditions.GetReason(resourceObjNotReady, "Ready")
				if reason != v1alpha1.ResourceIsNotAvailable {
					return fmt.Errorf("expected not-ready resource reason %s, got %s", v1alpha1.ResourceIsNotAvailable, reason)
				}

				return nil
			}, "15s").WithContext(ctx).Should(Succeed())

			By("deleting the resource")
			deleteResource(ctx, resourceObj)
		})

		It("returns an appropriate error when the resource cannot be fetched", func(ctx SpecContext) {
			By("creating a CTF")
			ctfName := "resource-not-found"
			env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version(componentVersion, func() {
						env.Resource(resourceName, "1.0.0", artifacttypes.PLAIN_TEXT, ocmmetav1.LocalRelation, func() {
							env.BlobData(mime.MIME_TEXT, []byte("Hello World!"))
						})
					})
				})
			})

			ctfPath := filepath.Join(tempDir, ctfName)
			spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfPath)
			Expect(err).NotTo(HaveOccurred())
			specData, err := spec.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())

			By("mocking a component")
			componentObj = test.MockComponent(
				ctx,
				componentObjName,
				namespace.GetName(),
				&test.MockComponentOptions{
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      componentName,
						Version:        componentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
					},
					Repository: repositoryName,
				},
			)

			By("creating a resource")
			resourceObj := &v1alpha1.Resource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace.GetName(),
				},
				Spec: v1alpha1.ResourceSpec{
					ComponentRef: corev1.LocalObjectReference{
						Name: componentObj.GetName(),
					},
					Resource: v1alpha1.ResourceID{
						ByReference: v1alpha1.ResourceReference{
							Resource: ocmmetav1.NewIdentity("resource-not-found"),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resourceObj)).To(Succeed())

			By("checking that the resource has not been reconciled successfully")
			resourceObjNotReady := &v1alpha1.Resource{}
			Eventually(func(g Gomega, ctx context.Context) error {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resourceObj), resourceObjNotReady)
				if err != nil {
					return fmt.Errorf("failed to get resource: %w", err)
				}

				if conditions.IsReady(resourceObjNotReady) {
					return fmt.Errorf("resource %s is ready", resourceObjNotReady.Name)
				}

				reason := conditions.GetReason(resourceObjNotReady, "Ready")
				if reason != v1alpha1.GetOCMResourceFailedReason {
					return fmt.Errorf("expected not-ready resource reason %s, got %s", v1alpha1.GetOCMResourceFailedReason, reason)
				}

				return nil
			}, "15s").WithContext(ctx).Should(Succeed())

			By("deleting the resource")
			deleteResource(ctx, resourceObj)
		})

		// This test is checking that the resource is reconciled again when the status of the component changes.
		It("reconciles when the component is updated to ready status", func(ctx SpecContext) {
			By("creating a CTF")
			ctfName := "component-ready"
			env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version(componentVersion, func() {
						env.Resource(resourceName, "1.0.0", artifacttypes.OCI_ARTIFACT, ocmmetav1.ExternalRelation, func() {
							env.Access(ocmociartifact.New("ghcr.io/open-component-model/ocm/ocm.software/ocmcli/ocmcli-image:0.24.0"))
						})
					})
				})
			})

			ctfPath := filepath.Join(tempDir, ctfName)
			spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfPath)
			Expect(err).NotTo(HaveOccurred())
			specData, err := spec.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())

			By("mocking a component")
			componentObj = test.MockComponent(
				ctx,
				componentObjName,
				namespace.GetName(),
				&test.MockComponentOptions{
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      componentName,
						Version:        componentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
					},
					Repository: repositoryName,
				},
			)

			By("marking the mocked component as not ready")
			componentObjNotReady := &v1alpha1.Component{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(componentObj), componentObjNotReady)).To(Succeed())

			status.MarkNotReady(recorder, componentObjNotReady, v1alpha1.ResourceIsNotAvailable, "mock component is not ready")
			Expect(k8sClient.Status().Update(ctx, componentObjNotReady)).To(Succeed())

			By("creating a resource")
			resourceObj := &v1alpha1.Resource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace.GetName(),
				},
				Spec: v1alpha1.ResourceSpec{
					ComponentRef: corev1.LocalObjectReference{
						Name: componentObj.GetName(),
					},
					Resource: v1alpha1.ResourceID{
						ByReference: v1alpha1.ResourceReference{
							Resource: ocmmetav1.NewIdentity(resourceName),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resourceObj)).To(Succeed())

			By("checking that the resource has not been reconciled successfully")
			resourceObjNotReady := &v1alpha1.Resource{}
			Eventually(func(g Gomega, ctx context.Context) error {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resourceObj), resourceObjNotReady)
				if err != nil {
					return fmt.Errorf("failed to get resource: %w", err)
				}

				if conditions.IsReady(resourceObjNotReady) {
					return fmt.Errorf("resource %s is ready", resourceObjNotReady.Name)
				}

				reason := conditions.GetReason(resourceObjNotReady, "Ready")
				if reason != v1alpha1.ResourceIsNotAvailable {
					return fmt.Errorf("expected not-ready resource reason %s, got %s", v1alpha1.ResourceIsNotAvailable, reason)
				}

				return nil
			}, "15s").WithContext(ctx).Should(Succeed())

			By("updating the component to ready")
			componentObjReady := &v1alpha1.Component{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(componentObj), componentObjReady)).To(Succeed())

			status.MarkReady(recorder, componentObjReady, "mock component is ready")
			Expect(k8sClient.Status().Update(ctx, componentObjReady)).To(Succeed())

			By("checking that the resource has been reconciled successfully")
			waitUntilResourceIsReady(ctx, resourceObj)
			expectedSourceRef := &v1alpha1.SourceReference{
				Registry:   "ghcr.io",
				Repository: "open-component-model/ocm/ocm.software/ocmcli/ocmcli-image",
				Tag:        "0.24.0",
			}
			Expect(resourceObj.Status.Reference).To(Equal(expectedSourceRef))

			By("deleting the resource")
			deleteResource(ctx, resourceObj)
		})

		// This test checks if the resource is reconciled again, when the resource spec is updated.
		It("reconciles again when the resource changes", func(ctx SpecContext) {
			By("creating a CTF")
			ctfName := "resource-change"
			resourceVersionUpdated := "1.0.1"
			env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version(componentVersion, func() {
						env.Resource(resourceName, "1.0.0", artifacttypes.OCI_ARTIFACT, ocmmetav1.ExternalRelation, func() {
							env.Access(ocmociartifact.New("ghcr.io/open-component-model/ocm/ocm.software/ocmcli/ocmcli-image:0.23.0"))
						})
						env.Resource("resource-update", resourceVersionUpdated, artifacttypes.OCI_ARTIFACT, ocmmetav1.ExternalRelation, func() {
							env.Access(ocmociartifact.New("ghcr.io/open-component-model/ocm/ocm.software/ocmcli/ocmcli-image:0.24.0"))
						})
					})
				})
			})

			ctfPath := filepath.Join(tempDir, ctfName)
			spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfPath)
			Expect(err).NotTo(HaveOccurred())
			specData, err := spec.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())

			By("mocking a component")
			componentObj = test.MockComponent(
				ctx,
				componentObjName,
				namespace.GetName(),
				&test.MockComponentOptions{
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      componentName,
						Version:        componentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
					},
					Repository: repositoryName,
				},
			)

			By("creating a resource")
			resourceObj := &v1alpha1.Resource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace.GetName(),
				},
				Spec: v1alpha1.ResourceSpec{
					ComponentRef: corev1.LocalObjectReference{
						Name: componentObj.GetName(),
					},
					Resource: v1alpha1.ResourceID{
						ByReference: v1alpha1.ResourceReference{
							Resource: ocmmetav1.NewIdentity(resourceName),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resourceObj)).To(Succeed())

			By("checking that the resource has been reconciled successfully")
			waitUntilResourceIsReady(ctx, resourceObj)
			expectedSourceRef := &v1alpha1.SourceReference{
				Registry:   "ghcr.io",
				Repository: "open-component-model/ocm/ocm.software/ocmcli/ocmcli-image",
				Tag:        "0.23.0",
			}
			Expect(resourceObj.Status.Reference).To(Equal(expectedSourceRef))

			By("updating resource spec")
			resourceObjUpdate := &v1alpha1.Resource{}
			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(resourceObj), resourceObjUpdate)
			Expect(err).ToNot(HaveOccurred())

			resourceObjUpdate.Spec.Resource = v1alpha1.ResourceID{
				ByReference: v1alpha1.ResourceReference{
					Resource: ocmmetav1.NewIdentity("resource-update"),
				},
			}
			Expect(k8sClient.Update(ctx, resourceObjUpdate)).To(Succeed())

			By("checking that the updated resource has been reconciled successfully")
			resourceObjUpdated := &v1alpha1.Resource{}
			Eventually(func(g Gomega, ctx context.Context) error {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resourceObj), resourceObjUpdated)
				if err != nil {
					return fmt.Errorf("failed to get resource: %w", err)
				}

				if !conditions.IsReady(resourceObjUpdated) {
					return fmt.Errorf("resource %s is not ready", resourceObjUpdated.Name)
				}

				if resourceObjUpdated.Status.Resource == nil {
					return fmt.Errorf("resource %s has no resource status", resourceObjUpdated.Name)
				}

				if resourceObjUpdated.Status.Resource.Version != resourceVersionUpdated {
					return fmt.Errorf("resource %s resource-version in status is not updated", resourceObjUpdated.Name)
				}

				return nil
			}, "15s").WithContext(ctx).Should(Succeed())
			expectedSourceRef = &v1alpha1.SourceReference{
				Registry:   "ghcr.io",
				Repository: "open-component-model/ocm/ocm.software/ocmcli/ocmcli-image",
				Tag:        "0.24.0",
			}
			Expect(resourceObjUpdated.Status.Reference).To(Equal(expectedSourceRef))

			By("deleting the resource")
			deleteResource(ctx, resourceObj)
		})

		// In this test the component version is updated with a new resource. This should trigger the control-loop of
		// the resource and we expect an updated source reference.
		It("reconciles again when the component and resource changes", func(ctx SpecContext) {
			By("creating a CTF")
			ctfName := "component-change"
			env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version(componentVersion, func() {
						env.Resource(resourceName, "1.0.0", artifacttypes.OCI_ARTIFACT, ocmmetav1.ExternalRelation, func() {
							env.Access(ocmociartifact.New("ghcr.io/open-component-model/ocm/ocm.software/ocmcli/ocmcli-image:0.23.0"))
						})
					})
				})
			})

			ctfPath := filepath.Join(tempDir, ctfName)
			spec, err := ctf.NewRepositorySpec(ctf.ACC_READONLY, ctfPath)
			Expect(err).NotTo(HaveOccurred())
			specData, err := spec.MarshalJSON()
			Expect(err).NotTo(HaveOccurred())

			By("mocking a component")
			componentObj = test.MockComponent(
				ctx,
				componentObjName,
				namespace.GetName(),
				&test.MockComponentOptions{
					Client:   k8sClient,
					Recorder: recorder,
					Info: v1alpha1.ComponentInfo{
						Component:      componentName,
						Version:        componentVersion,
						RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
					},
					Repository: repositoryName,
				},
			)

			By("creating a resource")
			resourceObj := &v1alpha1.Resource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace.GetName(),
				},
				Spec: v1alpha1.ResourceSpec{
					ComponentRef: corev1.LocalObjectReference{
						Name: componentObj.GetName(),
					},
					Resource: v1alpha1.ResourceID{
						ByReference: v1alpha1.ResourceReference{
							Resource: ocmmetav1.NewIdentity(resourceName),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resourceObj)).To(Succeed())

			By("checking that the resource has been reconciled successfully")
			waitUntilResourceIsReady(ctx, resourceObj)
			expectedSourceRef := &v1alpha1.SourceReference{
				Registry:   "ghcr.io",
				Repository: "open-component-model/ocm/ocm.software/ocmcli/ocmcli-image",
				Tag:        "0.23.0",
			}
			Expect(resourceObj.Status.Reference).To(Equal(expectedSourceRef))

			By("updating the component version with a new resource")
			componentVersionUpdated := "v1.0.1"
			env.OCMCommonTransport(ctfName, accessio.FormatDirectory, func() {
				env.Component(componentName, func() {
					env.Version(componentVersion, func() {
						env.Resource(resourceName, "1.0.0", artifacttypes.OCI_ARTIFACT, ocmmetav1.ExternalRelation, func() {
							env.Access(ocmociartifact.New("ghcr.io/open-component-model/ocm/ocm.software/ocmcli/ocmcli-image:0.23.0"))
						})
					})
				})
				env.Component(componentName, func() {
					env.Version(componentVersionUpdated, func() {
						env.Resource(resourceName, "1.0.1", artifacttypes.OCI_ARTIFACT, ocmmetav1.ExternalRelation, func() {
							env.Access(ocmociartifact.New("ghcr.io/open-component-model/ocm/ocm.software/ocmcli/ocmcli-image:0.24.0"))
						})
					})
				})
			})

			By("updating mock component status")
			componentObj = &v1alpha1.Component{
				ObjectMeta: k8smetav1.ObjectMeta{
					Namespace: componentObj.GetNamespace(),
					Name:      componentObj.GetName(),
				},
			}
			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(componentObj), componentObj)
			Expect(err).ToNot(HaveOccurred())

			componentObj.Status.Component.Version = componentVersionUpdated
			Expect(k8sClient.Status().Update(ctx, componentObj)).To(Succeed())

			By("updating mock component spec")
			componentObj = &v1alpha1.Component{
				ObjectMeta: k8smetav1.ObjectMeta{
					Namespace: componentObj.GetNamespace(),
					Name:      componentObj.GetName(),
				},
			}
			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(componentObj), componentObj)
			Expect(err).ToNot(HaveOccurred())

			componentObj.Spec.Semver = componentVersionUpdated
			Expect(k8sClient.Update(ctx, componentObj)).To(Succeed())

			// component spec update should trigger resource reconciliation
			By("checking that the resource was reconciled again")
			resourceObjUpdated := &v1alpha1.Resource{}
			Eventually(func(g Gomega, ctx context.Context) error {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resourceObj), resourceObjUpdated)
				if err != nil {
					return fmt.Errorf("failed to get resource: %w", err)
				}

				if !conditions.IsReady(resourceObjUpdated) {
					return fmt.Errorf("resource %s is not ready", resourceObjUpdated.Name)
				}

				if resourceObjUpdated.Status.Component == nil {
					return fmt.Errorf("resource %s component in status is nil", resourceObjUpdated.Name)
				}

				if resourceObjUpdated.Status.Component.Version != componentVersionUpdated {
					return fmt.Errorf("resource %s component version in status is not updated", resourceObjUpdated.Name)
				}

				return nil
			}, "15s").WithContext(ctx).Should(Succeed())
			expectedSourceRef = &v1alpha1.SourceReference{
				Registry:   "ghcr.io",
				Repository: "open-component-model/ocm/ocm.software/ocmcli/ocmcli-image",
				Tag:        "0.24.0",
			}
			Expect(resourceObjUpdated.Status.Reference).To(Equal(expectedSourceRef))

			By("deleting the resource")
			deleteResource(ctx, resourceObj)
		})
	})
})

func deleteResource(ctx context.Context, resource *v1alpha1.Resource) {
	GinkgoHelper()

	Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

	Eventually(func(ctx context.Context) error {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}

		return fmt.Errorf("resource %s still exists", resource.Name)
	}, "15s").WithContext(ctx).Should(Succeed())
}

func waitUntilResourceIsReady(ctx context.Context, resource *v1alpha1.Resource) {
	GinkgoHelper()

	Eventually(func(g Gomega, ctx context.Context) error {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), resource)
		if err != nil {
			return err
		}
		g.Expect(resource).Should(HaveField("Status.Resource", Not(BeNil())))

		if !conditions.IsReady(resource) {
			return fmt.Errorf("resource %s is not ready", resource.Name)
		}

		return nil
	}, "15s").WithContext(ctx).Should(Succeed())
}

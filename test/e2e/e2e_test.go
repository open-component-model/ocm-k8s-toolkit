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

package e2e

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/require"

	"github.com/open-component-model/ocm-k8s-toolkit/test/utils"
)

const (
	ComponentConstructor = "component-constructor.yaml"
	Bootstrap            = "bootstrap.yaml"
	Rgd                  = "rgd.yaml"
	Instance             = "instance.yaml"
	PublicKey            = "ocm.software.pub"
	PrivateKey           = "ocm.software"
)

var _ = Describe("controller", func() {
	Context("examples", func() {
		t := GinkgoT()

		for _, example := range examples {
			fInfo, err := os.Stat(filepath.Join(examplesDir, example.Name()))
			Expect(err).NotTo(HaveOccurred())
			if !fInfo.IsDir() {
				continue
			}

			reqFiles := []string{ComponentConstructor, Bootstrap, Rgd, Instance}

			It("should deploy the example "+example.Name(), func() {
				r := require.New(t)

				By("validating the example directory " + example.Name())
				var files []string
				Expect(filepath.WalkDir(
					filepath.Join(examplesDir, example.Name()),
					func(path string, d os.DirEntry, err error) error {
						if err != nil {
							return err
						}
						if d.IsDir() {
							return nil
						}
						files = append(files, d.Name())
						return nil
					})).To(Succeed())

				r.Subsetf(files, reqFiles, "required files %s not found in example directory %s", reqFiles, example.Name())

				By("creating and transferring a component version for " + example.Name())
				// If directory contains a private key, the component version is signed.
				signingKey := ""
				if slices.Contains(files, PrivateKey) {
					signingKey = filepath.Join(examplesDir, example.Name(), PrivateKey)
				}
				Expect(utils.PrepareOCMComponent(
					example.Name(),
					filepath.Join(examplesDir, example.Name(), ComponentConstructor),
					imageRegistry,
					signingKey,
				)).To(Succeed())

				By("bootstrapping the example")
				Expect(utils.DeployResource(filepath.Join(examplesDir, example.Name(), Bootstrap))).To(Succeed())
				name := "rgd/" + example.Name()
				Expect(utils.WaitForResource("create", timeout, name)).To(Succeed())
				Expect(utils.WaitForResource("condition=ReconcilerReady=true", timeout, name)).To(Succeed())
				Expect(utils.WaitForResource("condition=GraphVerified=true", timeout, name)).To(Succeed())
				Expect(utils.WaitForResource("condition=CustomResourceDefinitionSynced=true", timeout, name)).To(Succeed())

				By("creating an instance of the example")
				Expect(utils.DeployAndWaitForResource(
					filepath.Join(examplesDir, example.Name(), Instance),
					"condition=InstanceSynced=true",
					timeout,
				)).To(Succeed())

				By("validating the example")
				name = "deployment.apps/" + example.Name() + "-podinfo"
				Expect(utils.WaitForResource("create", timeout, name)).To(Succeed())
				Expect(utils.WaitForResource("condition=Available", timeout, name)).To(Succeed())
				Expect(utils.WaitForResource(
					"condition=Ready=true",
					timeout,
					"pod", "-l", "app.kubernetes.io/name="+example.Name()+"-podinfo",
				)).To(Succeed())

				switch example.Name() {
				case "helm-configuration-localisation":
					By("validating the localisation")
					Expect(utils.CompareResourceField(
						"'{.items[0].spec.containers[0].image}'",
						strings.TrimLeft(imageRegistry, "http://")+"/stefanprodan/podinfo:6.7.1",
						"pod", "-l", "app.kubernetes.io/name="+example.Name()+"-podinfo",
					)).To(Succeed())
					By("validating the configuration")
					Expect(utils.CompareResourceField(
						"'{.items[0].spec.containers[0].env[?(@.name==\"PODINFO_UI_MESSAGE\")].value}'",
						example.Name(),
						"pod", "-l", "app.kubernetes.io/name="+example.Name()+"-podinfo",
					)).To(Succeed())
				}
			})
		}
	})
})

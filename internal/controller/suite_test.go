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
	"fmt"
	. "github.com/mandelsoft/goutils/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/open-component-model/ocm-k8s-toolkit/utils/helpers"
	"github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/server"
	"k8s.io/client-go/tools/record"
	"os"
	"path/filepath"
	"runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	metricserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

const (
	ARTIFACT_PATH   = "ocm-k8s-artifactstore--*"
	ARTIFACT_SERVER = "localhost:8080"
)

var cfg *rest.Config
var k8sClient client.Client
var k8sManager ctrl.Manager
var testEnv *envtest.Environment

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.30.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	DeferCleanup(testEnv.Stop)

	Expect(deliveryv1alpha1.AddToScheme(scheme.Scheme)).Should(Succeed())
	Expect(v1alpha1.AddToScheme(scheme.Scheme)).Should(Succeed())
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	komega.SetClient(k8sClient)

	k8sManager, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metricserver.Options{
			BindAddress: "0",
		},
	})
	Expect(err).ToNot(HaveOccurred())

	tmpdir := Must(os.MkdirTemp("", ARTIFACT_PATH))
	address := ARTIFACT_SERVER
	storage := Must(server.NewStorage(k8sClient, testEnv.Scheme, tmpdir, address, 0, 0))
	artifactServer := Must(server.NewArtifactServer(tmpdir, address, time.Millisecond))

	// Register reconcilers
	//Expect((&OCMRepositoryReconciler{
	//	GetClient: k8sClient,
	//	Scheme: testEnv.Scheme,
	//}).SetupWithManager(k8sManager)).To(Succeed())

	Expect((&ComponentReconciler{
		OCMK8SBaseReconciler: &helpers.OCMK8SBaseReconciler{
			Client: k8sClient,
			Scheme: testEnv.Scheme,
			EventRecorder: &record.FakeRecorder{
				Events:        make(chan string, 32),
				IncludeObject: true,
			},
		},
		Storage: storage,
	}).SetupWithManager(k8sManager)).To(Succeed())

	//Expect((&ResourceReconciler{
	//	GetClient: k8sClient,
	//	Scheme: testEnv.Scheme,
	//}).SetupWithManager(k8sManager)).To(Succeed())

	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	go func() {
		defer GinkgoRecover()
		Expect(artifactServer.Start(ctx)).To(Succeed())
	}()
	go func() {
		defer GinkgoRecover()
		Expect(k8sManager.Start(ctx)).To(Succeed())
	}()
})

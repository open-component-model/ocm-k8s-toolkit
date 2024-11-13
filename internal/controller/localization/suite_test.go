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

package localization

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"github.com/openfluxcd/controller-manager/server"
	"github.com/openfluxcd/controller-manager/storage"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/configuration"
	cfgclient "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/configuration/client"
	locclient "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/client"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/test"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

const (
	ARTIFACT_SERVER = "localhost:0"
	ARTIFACT_CRD    = "https://raw.githubusercontent.com/openfluxcd/artifact/refs/heads/main/config/crd/bases/openfluxcd.ocm.software_artifacts.yaml"
)

var cfg *rest.Config
var k8sClient client.Client
var k8sManager ctrl.Manager
var testEnv *envtest.Environment
var strg *storage.Storage
var recorder record.EventRecorder

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Localization Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	artifactCRD, err := test.ParseCRD(serializer.NewCodecFactory(scheme.Scheme).UniversalDeserializer(), func() ([]byte, error) {
		resp, err := http.Get(ARTIFACT_CRD)
		if err != nil {
			return []byte{}, fmt.Errorf("failed to download CRD: %w", err)
		}
		DeferCleanup(func() error {
			return resp.Body.Close()
		})

		return io.ReadAll(resp.Body)
	})
	Expect(err).NotTo(HaveOccurred())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,

		CRDs: []*apiextensionsv1.CustomResourceDefinition{artifactCRD},

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "..", "bin", "k8s",
			fmt.Sprintf("1.30.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	DeferCleanup(testEnv.Stop)

	Expect(v1alpha1.AddToScheme(scheme.Scheme)).Should(Succeed())
	Expect(artifactv1.AddToScheme(scheme.Scheme)).Should(Succeed())

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

	tmpdir := GinkgoT().TempDir()
	Expect(err).ToNot(HaveOccurred())
	address := ARTIFACT_SERVER
	strg, err = server.NewStorage(k8sClient, testEnv.Scheme, tmpdir, address, 0, 0)
	Expect(err).ToNot(HaveOccurred())
	artifactServer, err := server.NewArtifactServer(tmpdir, address, time.Millisecond)
	Expect(err).ToNot(HaveOccurred())

	recorder = &record.FakeRecorder{
		Events:        make(chan string, 32),
		IncludeObject: true,
	}

	Expect((&Reconciler{
		BaseReconciler: &ocm.BaseReconciler{
			Client:        k8sClient,
			Scheme:        testEnv.Scheme,
			EventRecorder: recorder,
		},
		LocalizationClient: locclient.NewClientWithLocalStorage(k8sClient, strg, scheme.Scheme),
		Storage:            strg,
	}).SetupWithManager(k8sManager)).To(Succeed())

	Expect((&configuration.Reconciler{
		BaseReconciler: &ocm.BaseReconciler{
			Client:        k8sClient,
			Scheme:        testEnv.Scheme,
			EventRecorder: recorder,
		},
		ConfigClient: cfgclient.NewClientWithLocalStorage(k8sClient, strg, scheme.Scheme),
		Storage:      strg,
	}).SetupWithManager(k8sManager)).To(Succeed())

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

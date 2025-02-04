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

package component

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	. "github.com/mandelsoft/goutils/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/snapshot"
)

// +kubebuilder:scaffold:imports

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var k8sManager ctrl.Manager
var testEnv *envtest.Environment
var zotCmd *exec.Cmd
var registry *snapshot.Registry
var zotRootDir string

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "..", "bin", "k8s",
			fmt.Sprintf("1.30.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	var err error

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

	// Create zot-registry config file
	zotRootDir = Must(os.MkdirTemp("", ""))
	zotAddress := "0.0.0.0"
	zotPort := "8080"
	zotConfig := []byte(fmt.Sprintf(`{"storage":{"rootDirectory":"%s"},"http":{"address":"%s","port": "%s"}}`, zotRootDir, zotAddress, zotPort))
	zotConfigFile := filepath.Join(zotRootDir, "config.json")
	MustBeSuccessful(os.WriteFile(zotConfigFile, zotConfig, 0644))

	// Start zot-registry
	zotCmd = exec.Command(filepath.Join("..", "..", "..", "bin", "zot-registry"), "serve", zotConfigFile)
	err = zotCmd.Start()
	Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Failed to start Zot"))

	// Wait for Zot to be ready
	Eventually(func() error {
		resp, err := http.Get(fmt.Sprintf("http://%s:%s/v2/", zotAddress, zotPort))
		if err != nil {
			return fmt.Errorf("could not connect to Zot")
		}

		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}

		return nil
	}, 30*time.Second, 1*time.Second).Should(Succeed(), "Zot registry did not start in time")

	registry, err = snapshot.NewRegistry(fmt.Sprintf("%s:%s", zotAddress, zotPort))
	registry.PlainHTTP = true

	Expect((&Reconciler{
		BaseReconciler: &ocm.BaseReconciler{
			Client: k8sClient,
			Scheme: testEnv.Scheme,
			EventRecorder: &record.FakeRecorder{
				Events:        make(chan string, 32),
				IncludeObject: true,
			},
		},
		Registry: registry,
	}).SetupWithManager(k8sManager)).To(Succeed())

	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: Namespace,
		},
	}
	Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(k8sManager.Start(ctx)).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	if zotCmd != nil {
		err := zotCmd.Process.Kill()
		Expect(err).NotTo(HaveOccurred(), "Failed to stop Zot registry")

		// Clean up root directory
		MustBeSuccessful(os.RemoveAll(zotRootDir))
	}
})

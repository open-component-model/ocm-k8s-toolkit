package deployer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"testing"

	krov1alpha1 "github.com/kro-run/kro/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/yaml"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/ocm"
)

// +kubebuilder:scaffold:imports

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var k8sClient client.Client
var k8sManager ctrl.Manager
var testEnv *envtest.Environment
var recorder record.EventRecorder
var ctx context.Context
var cancel context.CancelFunc

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	// Get external CRD for the ResourceGraphDefinition
	resp, err := http.Get("https://raw.githubusercontent.com/kro-run/kro/refs/tags/v0.2.3/config/crd/bases/kro.run_resourcegraphdefinitions.yaml")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() error {
		return resp.Body.Close()
	})

	crdBytes, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())

	rgdCRD := &apiextensionsv1.CustomResourceDefinition{}
	err = yaml.Unmarshal(crdBytes, rgdCRD)
	Expect(err).NotTo(HaveOccurred())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "config", "crd", "bases"),
		},
		ErrorIfCRDPathMissing: true,

		CRDs: []*apiextensionsv1.CustomResourceDefinition{rgdCRD},

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "..", "bin", "k8s",
			fmt.Sprintf("1.30.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	// cfg is defined in this file globally.
	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())
	DeferCleanup(testEnv.Stop)

	Expect(v1alpha1.AddToScheme(scheme.Scheme)).Should(Succeed())
	Expect(krov1alpha1.AddToScheme(scheme.Scheme)).Should(Succeed())

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

	ctx, cancel = context.WithCancel(context.Background())
	DeferCleanup(cancel)

	events := make(chan string)
	recorder = &record.FakeRecorder{
		Events:        events,
		IncludeObject: true,
	}

	go func() {
		for {
			select {
			case event := <-events:
				GinkgoLogr.Info("Event received", "event", event)
			case <-ctx.Done():
				return
			}
		}
	}()

	Expect((&Reconciler{
		BaseReconciler: &ocm.BaseReconciler{
			Client:        k8sManager.GetClient(),
			Scheme:        testEnv.Scheme,
			EventRecorder: recorder,
		},
	}).SetupWithManager(ctx, k8sManager)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(k8sManager.Start(ctx)).To(Succeed())
	}()
})

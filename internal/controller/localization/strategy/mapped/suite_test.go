package mapped

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

func TestConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "mapping (via ocm library) localization strategy test")
}

var _ = BeforeSuite(func() {
	Expect(v1alpha1.AddToScheme(scheme.Scheme)).Should(Succeed())
	Expect(artifactv1.AddToScheme(scheme.Scheme)).Should(Succeed())
})

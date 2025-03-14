package test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2" //nolint:stylecheck,revive // dot import is a typical pattern for using ginkgo
	. "github.com/onsi/gomega"    //nolint:stylecheck,revive // dot import is a typical pattern for using gomega

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
)

func ExpectArtifactToNotExist(ctx context.Context, registry *ociartifact.Registry, a *v1alpha1.OCIArtifactInfo) {
	GinkgoHelper()
	if a == nil {
		return
	}

	ociRepo, err := registry.NewRepository(ctx, a.Repository)
	Expect(err).NotTo(HaveOccurred())

	Eventually(func(ctx context.Context) error {
		exists, err := ociRepo.ExistsArtifact(ctx, a.Digest)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("artifact not expected to exist: %s/%s", a.Repository, a.Digest)
		}

		return nil
	}, "15s").WithContext(ctx).Should(Succeed())
}

package ocm

import (
	"context"

	"github.com/mandelsoft/goutils/sliceutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

const (
	SecretRef1 = "secret1"
	SecretRef2 = "secret2"
	SecretRef3 = "secret3"

	ConfigRef1 = "config1"
	ConfigRef2 = "config2"
	ConfigRef3 = "config3"

	Namespace = "default"
)

var (
	ConfigSet1 = "set1"
	ConfigSet2 = "set2"
)

var _ = Describe("k8s utils", func() {

	Context("get effective refs", func() {
		var (
			ctx context.Context

			repo      v1alpha1.OCMRepository
			defcomp   v1alpha1.Component
			nodefcomp v1alpha1.Component
		)

		BeforeEach(func() {
			ctx = context.Background()

			repo = v1alpha1.OCMRepository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: Namespace,
				},
				Spec: v1alpha1.OCMRepositorySpec{
					SecretRefs: []v1.LocalObjectReference{
						{Name: SecretRef1},
						{Name: SecretRef2},
						{Name: SecretRef3},
					},
					ConfigRefs: []v1.LocalObjectReference{
						{Name: ConfigRef1},
						{Name: ConfigRef2},
						{Name: ConfigRef3},
					},
					ConfigSet: &ConfigSet1,
				},
				Status: v1alpha1.OCMRepositoryStatus{
					SecretRefs: []v1.LocalObjectReference{
						{Name: SecretRef1},
						{Name: SecretRef2},
						{Name: SecretRef3},
					},
					ConfigRefs: []v1.LocalObjectReference{
						{Name: ConfigRef1},
						{Name: ConfigRef2},
						{Name: ConfigRef3},
					},
					ConfigSet: ConfigSet1,
				},
			}

			defcomp = v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: Namespace,
				},
				Spec: v1alpha1.ComponentSpec{},
			}

			nodefcomp = v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: Namespace,
				},
				Spec: v1alpha1.ComponentSpec{
					SecretRefs: []v1.LocalObjectReference{
						{Name: SecretRef1},
					},
					ConfigRefs: []v1.LocalObjectReference{
						{Name: ConfigRef1},
					},
					ConfigSet: &ConfigSet2,
				},
			}
		})

		It("get effective secret refs", func() {
			keys := GetEffectiveSecretRefs(ctx, &nodefcomp, &repo)
			expectedKeys := sliceutils.Transform(nodefcomp.Spec.SecretRefs, func(ref v1.LocalObjectReference) ctrl.ObjectKey {
				return ctrl.ObjectKey{Namespace: nodefcomp.Namespace, Name: ref.Name}
			})
			Expect(keys).To(ConsistOf(expectedKeys))
		})
		It("get effective secret refs by defaulting", func() {
			keys := GetEffectiveSecretRefs(ctx, &defcomp, &repo)
			expectedKeys := sliceutils.Transform(repo.Status.SecretRefs, func(ref v1.LocalObjectReference) ctrl.ObjectKey {
				return ctrl.ObjectKey{Namespace: repo.Namespace, Name: ref.Name}
			})
			Expect(keys).To(ConsistOf(expectedKeys))
		})
		It("get effective secret ref with no default", func() {
			keys := GetEffectiveSecretRefs(ctx, &repo)
			expectedKeys := sliceutils.Transform(repo.Spec.SecretRefs, func(ref v1.LocalObjectReference) ctrl.ObjectKey {
				return ctrl.ObjectKey{Namespace: repo.Namespace, Name: ref.Name}
			})
			Expect(keys).To(ConsistOf(expectedKeys))
		})
		It("get effective config refs", func() {
			keys := GetEffectiveConfigRefs(ctx, &nodefcomp, &repo)
			expectedKeys := sliceutils.Transform(nodefcomp.Spec.ConfigRefs, func(ref v1.LocalObjectReference) ctrl.ObjectKey {
				return ctrl.ObjectKey{Namespace: nodefcomp.Namespace, Name: ref.Name}
			})
			Expect(keys).To(ConsistOf(expectedKeys))
		})
		It("get effective config refs by defaulting", func() {
			keys := GetEffectiveConfigRefs(ctx, &defcomp, &repo)
			expectedKeys := sliceutils.Transform(repo.Status.ConfigRefs, func(ref v1.LocalObjectReference) ctrl.ObjectKey {
				return ctrl.ObjectKey{Namespace: repo.Namespace, Name: ref.Name}
			})
			Expect(keys).To(ConsistOf(expectedKeys))
		})
		It("get effective config refs with no default", func() {
			keys := GetEffectiveConfigRefs(ctx, &repo)
			expectedKeys := sliceutils.Transform(repo.Spec.ConfigRefs, func(ref v1.LocalObjectReference) ctrl.ObjectKey {
				return ctrl.ObjectKey{Namespace: repo.Namespace, Name: ref.Name}
			})
			Expect(keys).To(ConsistOf(expectedKeys))
		})
		It("get effective config set", func() {
			set := GetEffectiveConfigSet(ctx, &nodefcomp, &repo)
			expectedSet := *nodefcomp.Spec.ConfigSet
			Expect(set).To(Equal(expectedSet))
		})
		It("get effective config set by defaulting", func() {
			set := GetEffectiveConfigSet(ctx, &defcomp, &repo)
			expectedSet := *repo.Spec.ConfigSet
			Expect(set).To(Equal(expectedSet))
		})
	})
})

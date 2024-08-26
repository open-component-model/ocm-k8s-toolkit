package utils

import (
	"context"
	"github.com/mandelsoft/goutils/sliceutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	SECRET_REF1 = "secret1"
	SECRET_REF2 = "secret2"
	SECRET_REF3 = "secret3"

	CONFIG_REF1 = "config1"
	CONFIG_REF2 = "config2"
	CONFIG_REF3 = "config3"

	NAMESPACE = "default"
)

var (
	CONFIG_SET1 = "set1"
	CONFIG_SET2 = "set2"
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
					Namespace: NAMESPACE,
				},
				Spec: v1alpha1.OCMRepositorySpec{
					SecretRef: &v1.LocalObjectReference{
						Name: SECRET_REF1,
					},
					SecretRefs: []v1.LocalObjectReference{
						{Name: SECRET_REF2},
						{Name: SECRET_REF3},
					},
					ConfigRef: &v1.LocalObjectReference{
						Name: CONFIG_REF1,
					},
					ConfigRefs: []v1.LocalObjectReference{
						{Name: CONFIG_REF2},
						{Name: CONFIG_REF3},
					},
					ConfigSet: &CONFIG_SET1,
				},
				Status: v1alpha1.OCMRepositoryStatus{
					SecretRefs: []v1.LocalObjectReference{
						{Name: SECRET_REF1},
						{Name: SECRET_REF2},
						{Name: SECRET_REF3},
					},
					ConfigRefs: []v1.LocalObjectReference{
						{Name: CONFIG_REF1},
						{Name: CONFIG_REF2},
						{Name: CONFIG_REF3},
					},
					ConfigSet: CONFIG_SET1,
				},
			}

			defcomp = v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: NAMESPACE,
				},
				Spec: v1alpha1.ComponentSpec{},
			}

			nodefcomp = v1alpha1.Component{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: NAMESPACE,
				},
				Spec: v1alpha1.ComponentSpec{
					SecretRef: &v1.LocalObjectReference{
						Name: SECRET_REF1,
					},
					ConfigRef: &v1.LocalObjectReference{
						Name: CONFIG_REF1,
					},
					ConfigSet: &CONFIG_SET2,
				},
			}
		})

		It("get effective secret refs", func() {
			keys := GetEffectiveSecretRefs(ctx, &nodefcomp, &repo)
			expectedKeys := sliceutils.Transform(append(nodefcomp.Spec.SecretRefs, *nodefcomp.Spec.SecretRef), func(ref v1.LocalObjectReference) ctrl.ObjectKey {
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
			expectedKeys := sliceutils.Transform(append(repo.Spec.SecretRefs, *repo.Spec.SecretRef), func(ref v1.LocalObjectReference) ctrl.ObjectKey {
				return ctrl.ObjectKey{Namespace: repo.Namespace, Name: ref.Name}
			})
			Expect(keys).To(ConsistOf(expectedKeys))
		})
		It("get effective config refs", func() {
			keys := GetEffectiveConfigRefs(ctx, &nodefcomp, &repo)
			expectedKeys := sliceutils.Transform(append(nodefcomp.Spec.ConfigRefs, *nodefcomp.Spec.ConfigRef), func(ref v1.LocalObjectReference) ctrl.ObjectKey {
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
			expectedKeys := sliceutils.Transform(append(repo.Spec.ConfigRefs, *repo.Spec.ConfigRef), func(ref v1.LocalObjectReference) ctrl.ObjectKey {
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

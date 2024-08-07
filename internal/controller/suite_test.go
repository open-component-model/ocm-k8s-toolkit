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
	"testing"
	"time"

	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/compdesc"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/fakes"
)

type testEnv struct {
	scheme *runtime.Scheme
	obj    []client.Object
}

// FakeKubeClientOption defines options to construct a fake kube client. There are some defaults involved.
// Scheme gets corev1 and v1alpha1 schemes by default. Anything that is passed in will override current
// defaults.
type FakeKubeClientOption func(testEnv *testEnv)

// WithAddToScheme adds the scheme.
func WithAddToScheme(addToScheme func(s *runtime.Scheme) error) FakeKubeClientOption {
	return func(testEnv *testEnv) {
		if err := addToScheme(testEnv.scheme); err != nil {
			panic(err)
		}
	}
}

// WithObjects provides an option to set objects for the fake client.
func WithObjects(obj ...client.Object) FakeKubeClientOption {
	return func(testEnv *testEnv) {
		testEnv.obj = obj
	}
}

// FakeKubeClient creates a fake kube client with some defaults and optional arguments.
func (t *testEnv) FakeKubeClient(opts ...FakeKubeClientOption) client.Client {
	for _, o := range opts {
		o(t)
	}
	return fake.NewClientBuilder().
		WithScheme(t.scheme).
		WithObjects(t.obj...).
		WithStatusSubresource(t.obj...).
		Build()
}

// FakeKubeClient creates a fake kube client with some defaults and optional arguments.
func (t *testEnv) FakeDynamicKubeClient(
	opts ...FakeKubeClientOption,
) *fakedynamic.FakeDynamicClient {
	for _, o := range opts {
		o(t)
	}
	var objs []runtime.Object
	for _, t := range t.obj {
		objs = append(objs, t)
	}
	return fakedynamic.NewSimpleDynamicClient(t.scheme, objs...)
}

var (
	DefaultComponent = &v1alpha1.Component{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ComponentVersion",
			APIVersion: v1alpha1.GroupVersion.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-component",
			Namespace: "default",
		},
		Spec: v1alpha1.ComponentSpec{
			Interval:  metav1.Duration{Duration: 10 * time.Minute},
			Component: "github.com/open-component-model/test-component",
			RepositoryRef: v1alpha1.ObjectKey{
				Namespace: "default",
				Name:      "test-repository",
			},
			Semver: "v0.1.0",
			Verify: []v1alpha1.Verification{},
		},
	}
	DefaultResource = &v1alpha1.Resource{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Resource",
			APIVersion: v1alpha1.GroupVersion.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-resource",
			Namespace: "default",
		},
		Spec: v1alpha1.ResourceSpec{
			Interval: metav1.Duration{Duration: 10 * time.Minute},
		},
	}
)

func getMockComponent(
	name, version string,
	opts ...fakes.AccessOptionFunc,
) ocm.ComponentVersionAccess {
	res := &fakes.Resource[*ocm.ResourceMeta]{
		Name:          "introspect-image",
		Version:       "1.0.0",
		Type:          "ociImage",
		Relation:      "local",
		AccessOptions: opts,
	}
	comp := &fakes.Component{
		Name:      name,
		Version:   version,
		Resources: []*fakes.Resource[*ocm.ResourceMeta]{res},
	}
	res.Component = comp
	comp.ComponentDescriptor = fakes.ConstructComponentDescriptor(comp)

	return comp
}

func getMockComponentWithReference(
	name, version string,
	refName, refVersion string,
	opts ...fakes.AccessOptionFunc,
) ocm.ComponentVersionAccess {
	res := &fakes.Resource[*ocm.ResourceMeta]{
		Name:          "introspect-image",
		Version:       "1.0.0",
		Type:          "ociImage",
		Relation:      "local",
		AccessOptions: opts,
	}
	comp := &fakes.Component{
		Name:      name,
		Version:   version,
		Resources: []*fakes.Resource[*ocm.ResourceMeta]{res},
		References: map[string]ocm.ComponentReference{
			"embedded": {
				ElementMeta: compdesc.ElementMeta{
					Name:    refName,
					Version: refVersion,
				},
				ComponentName: refName,
			},
		},
	}
	res.Component = comp
	comp.ComponentDescriptor = fakes.ConstructComponentDescriptor(comp)

	return comp
}

var env *testEnv

func TestMain(m *testing.M) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = artifactv1.AddToScheme(scheme)

	env = &testEnv{
		scheme: scheme,
	}
	m.Run()
}

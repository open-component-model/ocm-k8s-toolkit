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
package ocm

import (
	_ "github.com/distribution/distribution/v3/registry/storage/driver/filesystem"
	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	Signature = "test-signature"
)

type testEnv struct {
	scheme *runtime.Scheme
	obj    []client.Object
}

// FakeKubeClientOption defines options to construct a fake kube client. There are some defaults involved.
// Scheme gets corev1 and v1alpha1 schemes by default. Anything that is passed in will override current
// defaults.
type FakeKubeClientOption func(testEnv *testEnv)

// WithObjects provides an option to set objects for the fake client.
func WithObjects(obj ...client.Object) FakeKubeClientOption {
	return func(testEnv *testEnv) {
		testEnv.obj = obj
	}
}

// FakeKubeClient creates a fake kube client with some defaults and optional arguments.
func (t *testEnv) FakeKubeClient(opts ...FakeKubeClientOption) client.Client {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	t.scheme = scheme

	for _, o := range opts {
		o(t)
	}
	return fake.NewClientBuilder().WithScheme(t.scheme).WithObjects(t.obj...).Build()
}

var env = testEnv{}

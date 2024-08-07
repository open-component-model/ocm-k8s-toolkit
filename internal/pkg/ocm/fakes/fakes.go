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

package fakes

import (
	"context"

	"github.com/go-logr/logr"
	"ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/cpi"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	ocmctrl "github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/ocm"
)

// MockOcmClient mocks OCM client. Sadly, no generated code can be used, because none of them understand
// not importing type aliased names that OCM uses. Meaning, external types request internally aliased
// resources and the mock does not compile.
// I.e.: counterfeiter: https://github.com/maxbrunsfeld/counterfeiter/issues/174
type MockOcmClient struct {
	getComponentVersionMap              map[string]ocm.ComponentVersionAccess
	getComponentVersionErr              error
	getComponentVersionCalledWith       [][]any
	getLatestComponentVersionVersion    string
	getLatestComponentVersionErr        error
	getLatestComponentVersionCalledWith [][]any
}

var _ ocmctrl.Contract = &MockOcmClient{}

func (m *MockOcmClient) CreateAuthenticatedOCMContext(ctx context.Context, obj *v1alpha1.OCMRepository) (ocm.Context, error) {
	return ocm.New(), nil
}

func (m *MockOcmClient) GetComponentVersion(ctx context.Context, octx ocm.Context, component *v1alpha1.Component, version string, repoConfig []byte) (cpi.ComponentVersionAccess, error) {
	m.getComponentVersionCalledWith = append(m.getComponentVersionCalledWith, []any{component, version, repoConfig})
	return m.getComponentVersionMap[component.Spec.Component], m.getComponentVersionErr
}

func (m *MockOcmClient) GetComponentVersionReturnsForName(name string, cva ocm.ComponentVersionAccess, err error) {
	if m.getComponentVersionMap == nil {
		m.getComponentVersionMap = make(map[string]ocm.ComponentVersionAccess)
	}
	m.getComponentVersionMap[name] = cva
	m.getComponentVersionErr = err
}

func (m *MockOcmClient) GetComponentVersionCallingArgumentsOnCall(i int) []any {
	return m.getComponentVersionCalledWith[i]
}

func (m *MockOcmClient) GetComponentVersionWasNotCalled() bool {
	return len(m.getComponentVersionCalledWith) == 0
}

func (m *MockOcmClient) GetLatestValidComponentVersion(ctx context.Context, octx ocm.Context, obj *v1alpha1.Component) (string, error) {
	m.getLatestComponentVersionCalledWith = append(m.getLatestComponentVersionCalledWith, []any{obj})
	return m.getLatestComponentVersionVersion, m.getLatestComponentVersionErr
}

func (m *MockOcmClient) GetLatestComponentVersionReturns(version string, err error) {
	m.getLatestComponentVersionVersion = version
	m.getLatestComponentVersionErr = err
}

func (m *MockOcmClient) GetLatestComponentVersionCallingArgumentsOnCall(i int) []any {
	return m.getLatestComponentVersionCalledWith[i]
}

func (m *MockOcmClient) GetLatestComponentVersionWasNotCalled() bool {
	return len(m.getLatestComponentVersionCalledWith) == 0
}

func (m *MockOcmClient) ListComponentVersions(logger logr.Logger, octx ocm.Context, obj *v1alpha1.Component) ([]ocmctrl.Version, error) {
	//TODO implement me
	panic("implement me")
}

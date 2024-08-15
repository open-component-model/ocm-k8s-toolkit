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

package event

import (
	"fmt"
	"testing"

	eventv1 "github.com/fluxcd/pkg/apis/event/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/tools/record"
)

func TestNewEvent(t *testing.T) {
	eventTests := []struct {
		description string
		severity    string
		expected    string
	}{
		{
			description: "event is of type info",
			severity:    eventv1.EventSeverityInfo,
			expected:    "Normal",
		},
		{
			description: "event is of type error",
			severity:    eventv1.EventSeverityError,
			expected:    "Warning",
		},
	}
	for i, tt := range eventTests {
		t.Run(fmt.Sprintf("%d: %s", i, tt.description), func(t *testing.T) {
			recorder := record.NewFakeRecorder(32)
			obj := &v1alpha1.Component{}
			conditions.MarkStalled(obj, v1alpha1.CheckVersionFailedReason, "err")
			conditions.MarkFalse(obj, meta.ReadyCondition, v1alpha1.CheckVersionFailedReason, "err")

			New(recorder, obj, nil, tt.severity, "msg")

			close(recorder.Events)
			for e := range recorder.Events {
				assert.Contains(t, e, "CheckVersionFailed")
				assert.Contains(t, e, tt.expected)
			}
		})
	}
}

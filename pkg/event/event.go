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
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"

	eventv1 "github.com/fluxcd/pkg/apis/event/v1beta1"
	corev1 "k8s.io/api/core/v1"
	kuberecorder "k8s.io/client-go/tools/record"
)

func New(recorder kuberecorder.EventRecorder, obj conditions.Getter, metadata map[string]string, severity, msg string, args ...any) {
	if metadata == nil {
		metadata = map[string]string{}
	}

	reason := severity
	if r := conditions.GetReason(obj, meta.ReadyCondition); r != "" {
		reason = r
	}

	eventType := corev1.EventTypeNormal
	if severity == eventv1.EventSeverityError {
		eventType = corev1.EventTypeWarning
	}

	recorder.AnnotatedEventf(obj, metadata, eventType, reason, msg, args...)
}

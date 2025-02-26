// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and Open Component Model contributors.
//
// SPDX-License-Identifier: Apache-2.0

package status

import (
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"

	eventv1 "github.com/fluxcd/pkg/apis/event/v1beta1"
	kuberecorder "k8s.io/client-go/tools/record"

	"github.com/open-component-model/ocm-k8s-toolkit/pkg/event"
)

// MarkNotReady sets the condition status of an Object to `Not Ready`.
func MarkNotReady(recorder kuberecorder.EventRecorder, obj conditions.Setter, reason, msg string) {
	conditions.Delete(obj, meta.ReconcilingCondition)
	conditions.MarkFalse(obj, meta.ReadyCondition, reason, msg) //nolint:govet // Error is currently not relevant
	event.New(recorder, obj, nil, eventv1.EventSeverityError, msg)
}

// MarkAsStalled sets the condition status of an Object to `Stalled`.
func MarkAsStalled(recorder kuberecorder.EventRecorder, obj conditions.Setter, reason, msg string) {
	conditions.Delete(obj, meta.ReconcilingCondition)
	conditions.MarkFalse(obj, meta.ReadyCondition, reason, msg) //nolint:govet // Error is currently not relevant
	conditions.MarkStalled(obj, reason, msg)                    //nolint:govet // Error is currently not relevant
	event.New(recorder, obj, nil, eventv1.EventSeverityError, msg)
}

// MarkReady sets the condition status of an Object to `Ready`.
func MarkReady(recorder kuberecorder.EventRecorder, obj conditions.Setter, msg string, messageArgs ...any) {
	conditions.MarkTrue(obj, meta.ReadyCondition, meta.SucceededReason, msg, messageArgs...)
	conditions.Delete(obj, meta.ReconcilingCondition)
	event.New(recorder, obj, nil, eventv1.EventSeverityInfo, msg, messageArgs...)
}

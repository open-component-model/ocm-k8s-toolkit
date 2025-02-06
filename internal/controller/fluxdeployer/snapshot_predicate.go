package fluxdeployer

import (
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

type SnapshotDigestChangedPredicate struct {
	predicate.Funcs
}

func (SnapshotDigestChangedPredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectOld == nil || e.ObjectNew == nil {
		return false
	}

	oldSnapshot, ok := e.ObjectOld.(*v1alpha1.Snapshot)
	if !ok {
		return false
	}

	newSnapshot, ok := e.ObjectNew.(*v1alpha1.Snapshot)
	if !ok {
		return false
	}

	if oldSnapshot.GetDigest() == "" && newSnapshot.GetDigest() != "" {
		return true
	}

	if oldSnapshot.GetDigest() != "" && newSnapshot.GetDigest() != "" &&
		(oldSnapshot.GetDigest() != newSnapshot.GetDigest()) {
		return true
	}

	return false
}

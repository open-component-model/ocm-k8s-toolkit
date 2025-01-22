package snapshot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fluxcd/pkg/runtime/conditions"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// generateName generates a name for a snapshot CR. If the name exceeds the character limit, it will be cut off at 256.
func generateName(obj v1alpha1.SnapshotWriter) string {
	name := strings.ToLower(fmt.Sprintf("%s-%s", obj.GetKind(), obj.GetName()))

	if len(name) > validation.DNS1123SubdomainMaxLength {
		return name[:validation.DNS1123SubdomainMaxLength]
	}

	return name
}

func Create(owner v1alpha1.SnapshotWriter, ociRepository, manifestDigest string, blob *v1alpha1.BlobInfo) *v1alpha1.Snapshot {
	return &v1alpha1.Snapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateName(owner),
			Namespace: owner.GetNamespace(),
		},
		Spec: v1alpha1.SnapshotSpec{
			Repository: ociRepository,
			Digest:     manifestDigest,
			Blob:       blob,
		},
		Status: v1alpha1.SnapshotStatus{},
	}
}

// ValidateSnapshotForOwner verifies if the snapshot for the given collectable is valid.
// This means that the snapshot must be present in the cluster the reader is connected to and
// the snapshot must be present in the OCI registry.
// Additionally, the passed digest must be different from the blob digest stored in the snapshot.
//
// This method can be used to determine if a snapshot needs an update or not because a snapshot that does not
// fulfill these conditions can be considered out of date (not in the cluster, not in the OCI registry, or mismatching
// digest).
func ValidateSnapshotForOwner(
	ctx context.Context,
	reader client.Reader,
	owner v1alpha1.SnapshotWriter,
	digest string,
) (bool, error) {
	// If the owner cannot return a snapshot name, the snapshot is not created yet.
	if owner.GetSnapshotName() == "" {
		return false, nil
	}

	snapshotResource := &v1alpha1.Snapshot{}
	err := reader.Get(ctx, types.NamespacedName{Namespace: owner.GetNamespace(), Name: owner.GetSnapshotName()}, snapshotResource)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if client.IgnoreNotFound(err) != nil {
		return false, fmt.Errorf("failed to get snapshot: %w", err)
	}

	if snapshotResource == nil {
		return false, nil
	}

	// Snapshot is only ready, if the repository exists in the OCI registry
	if !conditions.IsReady(snapshotResource) {
		return false, fmt.Errorf("snapshot %s is not ready", snapshotResource.GetName())
	}

	return snapshotResource.Spec.Blob.Digest != digest, nil
}

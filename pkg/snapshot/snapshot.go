package snapshot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	errorsK8s "k8s.io/apimachinery/pkg/api/errors"
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

func Create(owner v1alpha1.SnapshotWriter, ociRepository, manifestDigest, blobVersion, blobDigest string, blobSize int64) v1alpha1.Snapshot {
	return v1alpha1.Snapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateName(owner),
			Namespace: owner.GetNamespace(),
		},
		Spec: v1alpha1.SnapshotSpec{
			Repository: ociRepository,
			Digest:     manifestDigest,
			Blob: v1alpha1.BlobInfo{
				Digest: blobDigest,
				Tag:    blobVersion,
				Size:   blobSize,
			},
		},
		Status: v1alpha1.SnapshotStatus{},
	}
}

func GetSnapshotForOwner(ctx context.Context, clientK8s client.Client, owner any) (*v1alpha1.Snapshot, error) {
	ownerSnapshot, ok := owner.(v1alpha1.SnapshotWriter)
	if !ok {
		return nil, errors.New("owner is not a SnapshotWriter")
	}

	// List all snapshots in owners namespace
	var snapshots v1alpha1.SnapshotList

	if err := clientK8s.List(ctx, &snapshots, client.InNamespace(ownerSnapshot.GetNamespace())); err != nil {
		return nil, fmt.Errorf("failed to list snapshots: %w", err)
	}

	// Check for snapshot referenced by owner
	for _, snapshot := range snapshots.Items {
		for _, ref := range snapshot.ObjectMeta.OwnerReferences {
			if ownerSnapshot.GetUID() == ref.UID {
				return &snapshot, nil
			}
		}
	}

	return nil, errorsK8s.NewNotFound(schema.GroupResource{Resource: "snapshots"}, "snapshot not found")
}

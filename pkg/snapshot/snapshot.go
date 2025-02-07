package snapshot

import (
	"context"
	"fmt"
	"strings"

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

// TODO: TAKE A LOOK AGAIN
func GetSnapshotForOwner(ctx context.Context, clientK8s client.Client, owner v1alpha1.SnapshotWriter) (*v1alpha1.Snapshot, error) {
	snapshot := &v1alpha1.Snapshot{}

	if err := clientK8s.Get(ctx, types.NamespacedName{Name: owner.GetSnapshotName(), Namespace: owner.GetNamespace()}, snapshot); err != nil {
		return nil, err
	}

	return snapshot, nil
}

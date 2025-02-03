package v1alpha1

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SnapshotWriter defines any object which produces a snapshot
// +k8s:deepcopy-gen=false
type SnapshotWriter interface {
	client.Object
	GetSnapshotName() string
	GetKind() string
	GetNamespace() string
	GetName() string
}

// SnapshotSpec defines the desired state of Snapshot.
type SnapshotSpec struct {
	// OCI repository name
	// +required
	Repository string `json:"repository"`

	// Manifest digest (required to delete the manifest and prepare OCI artifact for GC)
	// +required
	Digest string `json:"digest"`

	// Blob
	// +required
	Blob BlobInfo `json:"blob"`

	// Suspend stops all operations on this object.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// SnapshotStatus defines the observed state of Snapshot.
type SnapshotStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Digest is calculated by the caching layer.
	// +optional
	LastReconciledDigest string `json:"digest,omitempty"`

	// Tag defines the explicit tag that was used to create the related snapshot and cache entry.
	// +optional
	LastReconciledTag string `json:"tag,omitempty"`

	// ObservedGeneration is the last reconciled generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

func (in *Snapshot) GetVID() map[string]string {
	metadata := make(map[string]string)
	metadata[GroupVersion.Group+"/snapshot_digest"] = in.Status.LastReconciledDigest

	return metadata
}

func (in *Snapshot) SetObservedGeneration(v int64) {
	in.Status.ObservedGeneration = v
}

// GetDigest returns the last reconciled digest for the snapshot.
func (in Snapshot) GetDigest() string {
	return in.Spec.Digest
}

// GetConditions returns the status conditions of the object.
func (in Snapshot) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the status conditions on the object.
func (in *Snapshot) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=snap
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description=""
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].message",description=""

// Snapshot is the Schema for the snapshots API.
type Snapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SnapshotSpec   `json:"spec,omitempty"`
	Status SnapshotStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SnapshotList contains a list of Snapshot.
type SnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Snapshot `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Snapshot{}, &SnapshotList{})
}

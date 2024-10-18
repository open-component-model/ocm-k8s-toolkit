package types

import (
	"io"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// LocalizationTarget is a target for localization.
// It contains the data that will be localized.
type LocalizationTarget interface {
	// Open returns a reader for instruction. It can be a tarball, a file, etc.
	// The caller is responsible for closing the reader.
	Open() (io.ReadCloser, error)
	// UnpackIntoDirectory unpacks the data into the given directory.
	// It returns an error if the directory already exists.
	// It returns an error if the source cannot be unpacked.
	UnpackIntoDirectory(path string) (err error)

	// GetDigest is the digest of the packed target in the form of '<algorithm>:<checksum>'.
	// It can be used to verify the integrity of the target or to identify it.
	GetDigest() string

	// GetRevision is a human-readable identifier traceable in the origin source system.
	// It can be a Git commit SHA, Git tag, a Helm chart version, etc.
	// It could be used to identify the target but ideally GetDigest should be used for this purpose
	GetRevision() string

	// GetResource returns the resource that is being localized.
	// It is currently necessary to scope the context of the localization
	// to the correct Component
	// TODO Technically this doesnt need to be there if the Target Resolution
	// is able to work with any component
	GetResource() *v1alpha1.Resource
}

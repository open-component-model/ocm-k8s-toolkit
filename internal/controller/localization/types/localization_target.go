package types

import (
	"io"
)

type LocalizationTarget interface {
	Open() (io.ReadCloser, error)
	UnpackIntoDirectory(path string) (err error)

	// GetDigest is the digest of the packed target in the form of '<algorithm>:<checksum>'.
	GetDigest() string
	// GetRevision is a human-readable identifier traceable in the origin source system.
	// It can be a Git commit SHA, Git tag, a Helm chart version, etc.
	GetRevision() string
}

package util

import (
	"fmt"
	"strings"

	"github.com/opencontainers/go-digest"
)

// RevisionAndDigest is an object that has a human-readable revision as well as a technical content digest.
// While it is usually possible to use a digest for a revision, the other way around is never a good idea.
type RevisionAndDigest interface {
	// GetRevision is a human-readable identifier traceable in the origin source system.
	// It can be a Git commit SHA, Git tag, a Helm chart version, etc.
	// It could be used to identify the source but ideally GetDigest should be used for this purpose
	GetRevision() string

	// GetDigest is the digest of the packed source in the form of '<algorithm>:<checksum>'.
	// It can be used to verify the integrity of the target or to identify it.
	GetDigest() (string, error)
}

// NewMappedRevisionAndDigest generates a MappedRevisionAndDigest based on 2 RevisionAndDigest combinations.
func NewMappedRevisionAndDigest(config, target RevisionAndDigest) (MappedRevisionAndDigest, error) {
	configDigest, err := config.GetDigest()
	if err != nil {
		return MappedRevisionAndDigest{}, err
	}
	targetDigest, err := target.GetDigest()
	if err != nil {
		return MappedRevisionAndDigest{}, err
	}

	return MappedRevisionAndDigest{
		ConfigRevision: config.GetRevision(),
		ConfigDigest:   configDigest,
		TargetRevision: target.GetRevision(),
		TargetDigest:   targetDigest,
	}, nil
}

// MappedRevisionAndDigest is a struct that represents a mapping between a config revision and a target revision.
// It can be used to uniquely identify a combination of applied configuration + target without digesting the whole content.
//
// That works by combining the individual digests with each other so that they represent a unique combination.
type MappedRevisionAndDigest struct {
	ConfigRevision string `json:"source"`
	TargetRevision string `json:"target"`
	ConfigDigest   string `json:"-"`
	TargetDigest   string `json:"-"`
}

func (r MappedRevisionAndDigest) String() string {
	return r.GetRevision()
}

func (r MappedRevisionAndDigest) GetDigest() (string, error) {
	return r.digest(), nil
}

func (r MappedRevisionAndDigest) digest() string {
	return digest.FromString(r.ConfigDigest + r.TargetDigest).String()
}

func (r MappedRevisionAndDigest) GetRevision() string {
	return fmt.Sprintf("%s configured with %s", r.TargetRevision, r.ConfigRevision)
}

func (r MappedRevisionAndDigest) ToArchiveFileName() string {
	return strings.ReplaceAll(r.digest(), ":", "_") + ".tar.gz"
}

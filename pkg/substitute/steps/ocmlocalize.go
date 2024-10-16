package steps

import (
	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/mandelsoft/vfs/pkg/projectionfs"
	ocmsubstitution "ocm.software/ocm/api/ocm/ocmutils/localize"
)

type ocmPathBasedSubstitutionStep struct {
	Substitutions ocmsubstitution.Substitutions
}

var _ Step = &ocmPathBasedSubstitutionStep{}

func NewOCMPathBasedSubstitutionStep(substitutions ocmsubstitution.Substitutions) Step {
	return &ocmPathBasedSubstitutionStep{
		Substitutions: substitutions,
	}
}

func (o *ocmPathBasedSubstitutionStep) Substitute(path string) error {
	fs, err := projectionfs.New(osfs.New(), path)
	if err != nil {
		return err
	}

	return ocmsubstitution.Substitute(o.Substitutions, fs)
}

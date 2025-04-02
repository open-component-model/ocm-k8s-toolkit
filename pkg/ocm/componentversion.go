package ocm

import (
	"fmt"

	ocmctx "ocm.software/ocm/api/ocm"
)

func GetComponentVersion(
	octx ocmctx.Context,
	session ocmctx.Session,
	specData []byte,
	componentName, componentVersion string,
) (ocmctx.ComponentVersionAccess, error) {
	spec, err := octx.RepositorySpecForConfig(specData, nil)
	if err != nil {
		return nil, err
	}

	repo, err := session.LookupRepository(octx, spec)
	if err != nil {
		return nil, fmt.Errorf("invalid repository spec: %w", err)
	}

	return session.LookupComponentVersion(repo, componentName, componentVersion)
}

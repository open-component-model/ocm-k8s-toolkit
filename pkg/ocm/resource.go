package ocm

import (
	"errors"
	"fmt"

	"ocm.software/ocm/api/ocm/compdesc"
	"ocm.software/ocm/api/ocm/extensions/attrs/signingattr"
	"ocm.software/ocm/api/ocm/selectors"
	"ocm.software/ocm/api/ocm/tools/signing"

	ocmctx "ocm.software/ocm/api/ocm"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
)

func GetResourceAccessForComponentVersion(
	cv ocmctx.ComponentVersionAccess,
	reference v1.ResourceReference,
	cdSet *Descriptors,
) (ocmctx.ResourceAccess, *compdesc.ComponentDescriptor, error) {
	// Resolve resource resourceReference to get resource and its component descriptor
	resourceDesc, resourceCompDesc, err := compdesc.ResolveResourceReference(cv.GetDescriptor(), reference, compdesc.NewComponentVersionSet(cdSet.List...))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve resource reference: %w", err)
	}

	resAccesses, err := cv.SelectResources(selectors.Identity(resourceDesc.GetIdentity(resourceCompDesc.GetResources())))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to select resources: %w", err)
	}

	var resourceAccess ocmctx.ResourceAccess
	switch len(resAccesses) {
	case 0:
		return nil, nil, errors.New("no resources selected")
	case 1:
		resourceAccess = resAccesses[0]
	default:
		return nil, nil, errors.New("cannot determine the resource access unambiguously")
	}

	// Consider option to omit verification (= resource download) if the resource is large
	if err := verifyResource(resourceAccess, cv, cv.GetDescriptor()); err != nil {
		return nil, nil, err
	}

	return resourceAccess, resourceCompDesc, nil
}

// verifyResource verifies the resource digest with the digest from the component version access and component descriptor.
func verifyResource(access ocmctx.ResourceAccess, cv ocmctx.ComponentVersionAccess, cd *compdesc.ComponentDescriptor) error {
	// TODO: https://github.com/open-component-model/ocm-k8s-toolkit/issues/71
	index := cd.GetResourceIndex(access.Meta())
	if index < 0 {
		return errors.New("resource not found in access spec")
	}
	raw := &cd.Resources[index]
	if raw.Digest == nil {
		return nil
	}

	// Create data access
	accessMethod, err := access.AccessMethod()
	if err != nil {
		return fmt.Errorf("failed to create access method: %w", err)
	}
	defer accessMethod.Close()

	// Add the component descriptor to the local verified store, so its digest will be compared with the digest from the
	// component version access
	store := signing.NewLocalVerifiedStore()
	store.Add(cd)

	ok, err := signing.VerifyResourceDigestByResourceAccess(cv, access, accessMethod.AsBlobAccess(), store)
	if !ok {
		if err != nil {
			return fmt.Errorf("verification failed: %w", err)
		}

		return errors.New("expected signature verification to be relevant, but it was not")
	}
	if err != nil {
		return fmt.Errorf("failed to verify resource digest: %w", err)
	}

	return nil
}

func GetResource(
	cv ocmctx.ComponentVersionAccess,
	resourceAccess ocmctx.ResourceAccess,
) (_ []byte, _ string, retErr error) {
	octx := cv.GetContext()
	cd := cv.GetDescriptor()
	raw := &cd.Resources[cd.GetResourceIndex(resourceAccess.Meta())]

	if raw.Digest == nil {
		return nil, "", errors.New("digest not found in resource access")
	}

	// Check if the resource is signature relevant
	acc, err := octx.AccessSpecForSpec(raw.Access)
	if err != nil {
		return nil, "", fmt.Errorf("failed getting access for resource: %w", err)
	}

	// What is the difference to resourceAccess.AccessMethod()?
	meth, err := acc.AccessMethod(cv)
	if err != nil {
		return nil, "", fmt.Errorf("failed getting access method: %w", err)
	}
	defer func() {
		retErr = meth.Close()
	}()

	// What is the difference to acc.AccessMethod(cv)?
	accessMethod, err := resourceAccess.AccessMethod()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create access method: %w", err)
	}
	defer func() {
		retErr = accessMethod.Close()
	}()

	bAcc := accessMethod.AsBlobAccess()

	meth = signing.NewRedirectedAccessMethod(meth, bAcc)
	resAccDigest := raw.Digest
	resAccDigestType := signing.DigesterType(resAccDigest)
	req := []ocmctx.DigesterType{resAccDigestType} // ????

	registry := signingattr.Get(octx).HandlerRegistry()
	hasher := registry.GetHasher(resAccDigestType.HashAlgorithm)
	digest, err := octx.BlobDigesters().DetermineDigests(raw.Type, hasher, registry, meth, req...)
	if err != nil {
		return nil, "", fmt.Errorf("failed determining digest for resource: %w", err)
	}

	// Get actual resource data
	data, err := bAcc.Get()
	if err != nil {
		return nil, "", fmt.Errorf("failed getting resource data: %w", err)
	}

	return data, digest[0].String(), nil
}

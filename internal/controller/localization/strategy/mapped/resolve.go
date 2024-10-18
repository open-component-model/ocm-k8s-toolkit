package mapped

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/containers/image/v5/pkg/compression"
	"github.com/google/go-containerregistry/pkg/name"
	ocmctx "ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/compdesc"
	ocmmetav1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/localblob"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociblob"
	"sigs.k8s.io/yaml"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

func unresolvedRefFromSource(source string, extraIdentity ocmmetav1.Identity) ocmmetav1.ResourceReference {
	identity := ocmmetav1.NewIdentity(source)
	var resourceReference ocmmetav1.ResourceReference
	if extraIdentity != nil {
		resourceReference = ocmmetav1.NewResourceRef(identity, extraIdentity)
	} else {
		resourceReference = ocmmetav1.NewResourceRef(identity)
	}

	return resourceReference
}

// resolveResourceReferenceFromComponentDescriptor takes
// - a localization rule
// - a component descriptor based on a resolver
// and returns the reference to the resource.
// This is what actually resolves the name of the resource to the actual image reference.
func resolveResourceReferenceFromComponentDescriptor(
	reference ocmmetav1.ResourceReference,
	componentDescriptor *compdesc.ComponentDescriptor,
	resolver compdesc.ComponentVersionResolver,
) (_ string, retErr error) {
	resourceFromRule, _, err := compdesc.ResolveResourceReference(componentDescriptor, reference, resolver)
	if err != nil {
		return "", fmt.Errorf("failed to resolve resource reference %s: %w", reference, err)
	}
	accessSpecification := resourceFromRule.GetAccess()

	var (
		ref    string
		refErr error
	)

	specInCtx, err := ocmctx.DefaultContext().AccessSpecForSpec(accessSpecification)
	if err != nil {
		return "", fmt.Errorf("failed to resolve access spec: %w", err)
	}

	// TODO this seems hacky but I copy & pasted, we need to find a better way
	for ref == "" && refErr == nil {
		switch x := specInCtx.(type) {
		case *ociartifact.AccessSpec:
			ref = x.ImageReference
		case *ociblob.AccessSpec:
			ref = fmt.Sprintf("%s@%s", x.Reference, x.Digest)
		case *localblob.AccessSpec:
			if x.GlobalAccess == nil {
				refErr = errors.New("cannot determine image digest")
			} else {
				// TODO maybe this needs whole OCM Context resolution?
				// I dont think we need a localized resolution here but Im not sure
				specInCtx = x.GlobalAccess.GlobalAccessSpec(ocmctx.DefaultContext())
			}
		default:
			refErr = errors.New("cannot determine access spec type")
		}
	}
	if refErr != nil {
		return "", fmt.Errorf("failed to parse access reference: %w", refErr)
	}

	return ref, nil
}

func valueFromTransformation(ref string, transformationType v1alpha1.TransformationType) (value string, err error) {
	parsed, err := name.ParseReference(ref)
	if err != nil {
		return "", fmt.Errorf("failed to parse access reference: %w", err)
	}
	switch transformationType {
	case v1alpha1.TransformationTypeRegistry:
		value = parsed.Context().Registry.RegistryStr()
	case v1alpha1.TransformationTypeRepository:
		value = parsed.Context().RepositoryStr()
	case v1alpha1.TransformationTypeTag:
		value = parsed.Identifier()
	case v1alpha1.TransformationTypeImageNoTag:
		value = parsed.Context().Name()
	case v1alpha1.TransformationTypeGoTemplate:
		return "", fmt.Errorf("unsupported transformation type for reference resolution: %s", transformationType)
	case v1alpha1.TransformationTypeImage:
		// By default treat the reference as a full image reference
		fallthrough
	default:
		value = parsed.Name()
	}

	return
}

// LocalizationConfigFromSource reads the localization config from the source.
// It autodecompresses the source and reads the config via DataFromTarOrPlain.
// This allows the source to be either
// - a plain yaml file in a (compressed or uncompressed) tar archive
// - a plain yaml file
//
// Note that if the tar archive contains multiple files, only the first one is read as stored in the tar.
func LocalizationConfigFromSource(source Source) (config *v1alpha1.LocalizationConfig, err error) {
	cfgReader, err := source.Open()
	defer func() {
		err = errors.Join(err, cfgReader.Close())
	}()
	decompressed, _, err := compression.AutoDecompress(cfgReader)
	if err != nil {
		return nil, fmt.Errorf("failed to autodecompress config: %w", err)
	}
	defer func() {
		err = errors.Join(err, decompressed.Close())
	}()
	data, err := DataFromTarOrPlain(decompressed)
	if err != nil {
		return nil, fmt.Errorf("failed to get data from tar or plain: %w", err)
	}

	cfg, err := io.ReadAll(data)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	err = yaml.Unmarshal(cfg, &config)

	return
}

// DataFromTarOrPlain checks if the reader is a tar archive and returns a new reader that reads from the beginning of the input.
// If the input is not a tar archive, the input reader is returned as is.
func DataFromTarOrPlain(reader io.Reader) (io.Reader, error) {
	isTar, reader := isTar(reader)
	if !isTar {
		return reader, nil
	}

	t := tar.NewReader(reader)
	fi, err := t.Next()
	if err != nil {
		return nil, fmt.Errorf("failed to read tar header even though data was recognized as tar: %w", err)
	}
	if fi.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("expected exactly one regular file in tar, got type flag %x", fi.Typeflag)
	}

	return t, nil
}

const TarPaddedHeaderSize = 512

// isTar checks if the reader is a tar archive and returns a new reader that reads from the beginning of the input.
// The input reader should no longer be used after calling this function.
func isTar(reader io.Reader) (bool, io.Reader) {
	var buf bytes.Buffer
	// We need to read the first 512 bytes to determine if the input is a tar archive.
	buf.Grow(TarPaddedHeaderSize)
	tr := tar.NewReader(io.TeeReader(reader, &buf))
	_, err := tr.Next()

	return err == nil, io.MultiReader(&buf, reader)
}

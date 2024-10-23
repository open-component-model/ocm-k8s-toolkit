package mapped

import (
	"errors"
	"fmt"
	"io"

	"github.com/containers/image/v5/pkg/compression"
	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/runtime"
	ocmctx "ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/compdesc"
	ocmmetav1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/localblob"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociblob"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/util"
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
// and returns the reference to the util.
// This is what actually resolves the name of the util to the actual image reference.
func resolveResourceReferenceFromComponentDescriptor(
	reference ocmmetav1.ResourceReference,
	componentDescriptor *compdesc.ComponentDescriptor,
	resolver compdesc.ComponentVersionResolver,
) (_ string, retErr error) {
	resourceFromRule, _, err := compdesc.ResolveResourceReference(componentDescriptor, reference, resolver)
	if err != nil {
		return "", fmt.Errorf("failed to resolve util reference %s: %w", reference, err)
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

// ParseLocalizationConfig reads the localization config from the source.
// It autodecompresses the source and reads the config via DataFromTarOrPlain.
// This allows the source to be either
// - a plain yaml file in a (compressed or uncompressed) tar archive
// - a plain yaml file
//
// Note that if the tar archive contains multiple files, only the first one is read as stored in the tar.
func ParseLocalizationConfig(source Config, decoder runtime.Decoder) (config LocalizationConfig, err error) {
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
	data, err := util.DataFromTarOrPlain(decompressed)
	if err != nil {
		return nil, fmt.Errorf("failed to Get data from tar or plain: %w", err)
	}

	cfg, err := io.ReadAll(data)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	obj, _, err := decoder.Decode(cfg, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}
	cfgObj, ok := obj.(LocalizationConfig)
	if !ok {
		return nil, fmt.Errorf("failed to decode config (not a valid LocalizationConfig): %w", err)
	}

	return cfgObj, nil
}

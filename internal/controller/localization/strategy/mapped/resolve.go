package mapped

import (
	"errors"
	"fmt"
	"io"

	"github.com/google/go-containerregistry/pkg/name"
	ocmctx "ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/compdesc"
	ocmmetav1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/localblob"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociblob"
	"ocm.software/ocm/api/ocm/ocmutils/localize"
	"ocm.software/ocm/api/utils/runtime"

	localizationv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/pkg/localization/v1alpha1"
)

func unresolvedRefFromRule(rule localizationv1alpha1.Rule, extraIdentity ocmmetav1.Identity) ocmmetav1.ResourceReference {
	identity := ocmmetav1.NewIdentity(rule.Source.Resource.Name)
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

// addResolvedRule adds the resolved rule to the substitutions that later work on the target files.
func addResolvedRule(substitutions *localize.Substitutions, rule localizationv1alpha1.Rule, ref string) error {
	var value string

	parsed, err := name.ParseReference(ref)
	if err != nil {
		return fmt.Errorf("failed to parse access reference: %w", err)
	}
	switch rule.Transformation.Type {
	case localizationv1alpha1.Registry:
		value = parsed.Context().Registry.Name()
	case localizationv1alpha1.Repository:
		value = parsed.Context().RepositoryStr()
	case localizationv1alpha1.Tag:
		value = parsed.Identifier()
	case localizationv1alpha1.Image:
		// By default treat the reference as a full image reference
		fallthrough
	default:
		value = parsed.Name()
	}

	return substitutions.Add("resource-reference", rule.Target.FileTarget.Path, rule.Target.FileTarget.Value, value)
}

func localizationConfigFromSource(source Source) (config *localizationv1alpha1.LocalizationConfig, err error) {
	cfgReader, err := source.Open()
	defer func() {
		err = errors.Join(err, cfgReader.Close())
	}()
	cfg, err := io.ReadAll(cfgReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	err = runtime.DefaultYAMLEncoding.Unmarshal(cfg, &config)

	return
}

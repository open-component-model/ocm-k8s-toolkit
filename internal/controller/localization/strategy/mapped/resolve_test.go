package mapped

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"ocm.software/ocm/api/ocm/compdesc"
	ocmmetav1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/types"
)

type MockedResolver struct {
	lookupComponentVersion func(name string, version string) (*compdesc.ComponentDescriptor, error)
}

var _ compdesc.ComponentVersionResolver = MockedResolver{}

func (m MockedResolver) LookupComponentVersion(name string, version string) (*compdesc.ComponentDescriptor, error) {
	return m.lookupComponentVersion(name, version)
}

func TestResolveResourceReferenceFromComponentDescriptor(t *testing.T) {
	// Happy path
	t.Run("resolves OCI artifact access spec", func(t *testing.T) {
		res := "source"
		ref := ocmmetav1.NewResourceRef(ocmmetav1.NewIdentity(res))
		componentDescriptor := &compdesc.ComponentDescriptor{}

		componentDescriptor.Resources = append(componentDescriptor.Resources, compdesc.Resource{
			ResourceMeta: compdesc.ResourceMeta{
				ElementMeta: compdesc.ElementMeta{
					Name:    res,
					Version: "1.0.0",
				},
			},
			Access: ociartifact.New("oci://example.com/image:tag"),
		})

		// Mock the resolver to return a util with OCI artifact access spec
		resolver := MockedResolver{
			lookupComponentVersion: func(name string, version string) (*compdesc.ComponentDescriptor, error) {
				return componentDescriptor, nil
			},
		}

		result, err := resolveResourceReferenceFromComponentDescriptor(ref, componentDescriptor, resolver)
		assert.NoError(t, err)
		assert.Equal(t, "oci://example.com/image:tag", result)
	})
}

func TestValueFromTransformation(t *testing.T) {
	// Happy path
	t.Run("returns registry from reference", func(t *testing.T) {
		ref := "example.com/repo/image:tag"
		transformationType := v1alpha1.TransformationTypeRegistry

		result, err := valueFromTransformation(ref, transformationType)
		assert.NoError(t, err)
		assert.Equal(t, "example.com", result)
	})
}

func TestParseLocalizationConfig(t *testing.T) {
	// Happy path
	t.Run("reads config from plain yaml file", func(t *testing.T) {
		source := &types.MockedLocalizationReference{
			Data: []byte("apiVersion: delivery.ocm.software/v1alpha1\nkind: LocalizationConfig\nmetadata:\n  name: test"),
		}
		schema := runtime.NewScheme()
		assert.NoError(t, v1alpha1.AddToScheme(schema))

		config, err := ParseLocalizationConfig(source, serializer.NewCodecFactory(schema).UniversalDeserializer())
		assert.NoError(t, err)
		assert.IsType(t, &v1alpha1.LocalizationConfig{}, config)
		typedConfig := config.(*v1alpha1.LocalizationConfig)
		assert.Equal(t, "test", typedConfig.GetName())
	})
}

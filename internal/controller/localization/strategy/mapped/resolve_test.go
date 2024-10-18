package mapped

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
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

		// Mock the resolver to return a resource with OCI artifact access spec
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

	// Edge case
	t.Run("returns error for unsupported transformation type", func(t *testing.T) {
		ref := "example.com/repo/image:tag"
		transformationType := v1alpha1.TransformationTypeGoTemplate

		_, err := valueFromTransformation(ref, transformationType)
		assert.Error(t, err)
	})
}

func TestLocalizationConfigFromSource(t *testing.T) {
	// Happy path
	t.Run("reads config from plain yaml file", func(t *testing.T) {
		source := types.NewLocalizationSourceWithStrategy(&types.MockedLocalizationReference{
			Data: []byte("apiVersion: v1alpha1\nkind: LocalizationConfig\nmetadata:\n  name: test"),
		}, v1alpha1.LocalizationStrategy{})

		config, err := LocalizationConfigFromSource(source)
		assert.NoError(t, err)
		assert.Equal(t, "test", config.GetName())
	})
}

func TestDataFromTarOrPlain(t *testing.T) {
	// Happy path
	t.Run("reads data from tar archive", func(t *testing.T) {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		tw.WriteHeader(&tar.Header{
			Name: "test.yaml",
			Size: int64(len("content")),
		})
		tw.Write([]byte("content"))
		tw.Close()

		reader, err := DataFromTarOrPlain(&buf)
		assert.NoError(t, err)

		data, err := io.ReadAll(reader)
		assert.NoError(t, err)
		assert.Equal(t, "content", string(data))
	})

	// Edge case
	t.Run("returns error for non-regular file in tar", func(t *testing.T) {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		tw.WriteHeader(&tar.Header{
			Name:     "test.yaml",
			Typeflag: tar.TypeDir,
		})
		tw.Close()

		_, err := DataFromTarOrPlain(&buf)
		assert.Error(t, err)
	})
}

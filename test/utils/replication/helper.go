package replication

import (
	"os"
	"time"

	mandelsoft "github.com/mandelsoft/goutils/testutils"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ocmbuilder "ocm.software/ocm/api/helper/builder"
	ocmmetav1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	"ocm.software/ocm/api/ocm/extensions/accessmethods/ociartifact"
	resourcetypes "ocm.software/ocm/api/ocm/extensions/artifacttypes"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/ocm/extensions/repositories/genericocireg"
	"ocm.software/ocm/api/ocm/extensions/repositories/ocireg"
	"ocm.software/ocm/api/utils/accessio"
	"sigs.k8s.io/yaml"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// "gcr.io/google_containers/echoserver:1.10" is taken as test content, because it is used to explain OCM here:
// https://ocm.software/docs/getting-started/getting-started-with-ocm/create-a-component-version/#add-an-image-reference-option-1
const TestResourceImage = "gcr.io/google_containers/echoserver:1.10"

// Reconciliation interval for the test resources created in this file.
const reconciliationInterval = time.Minute * 2

// The registry where OCM components of the OCM toolkit itself are stored can be used for read access in tests.
const (
	TestExternalRegistry  = "ghcr.io/open-component-model/ocm"
	TestExternalComponent = "ocm.software/ocmcli"
	TestExternalVersion   = "0.17.0"
)

func NewTestComponentVersionInCTFDir(env *ocmbuilder.Builder, path, compName, compVersion, img string) {
	env.OCMCommonTransport(path, accessio.FormatDirectory, func() {
		env.Component(compName, func() {
			env.Version(compVersion, func() {
				env.Resource("image", "1.0.0", resourcetypes.OCI_IMAGE, ocmmetav1.ExternalRelation, func() {
					env.Access(
						ociartifact.New(img),
					)
				})
			})
		})
	})
}

func NewTestCFTRepository(namespace, name, path string) (*v1alpha1.OCMRepository, *[]byte) {
	spec := mandelsoft.Must(ctf.NewRepositorySpec(ctf.ACC_CREATE, path))

	return newTestOCMRepository(namespace, name, spec)
}

func NewTestOCIRepository(namespace, name, url string) (*v1alpha1.OCMRepository, *[]byte) {
	spec := ocireg.NewRepositorySpec(url, nil)

	return newTestOCMRepository(namespace, name, spec)
}

func newTestOCMRepository(namespace, name string, spec *genericocireg.RepositorySpec) (*v1alpha1.OCMRepository, *[]byte) {
	specData := mandelsoft.Must(spec.MarshalJSON())

	return &v1alpha1.OCMRepository{
		TypeMeta: metav1.TypeMeta{
			Kind:       "OCMRepository",
			APIVersion: v1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: v1alpha1.OCMRepositorySpec{
			RepositorySpec: &apiextensionsv1.JSON{
				Raw: specData,
			},
			Interval: metav1.Duration{Duration: reconciliationInterval},
		},
	}, &specData
}

func NewTestComponent(namespace, name, repoName, ocmName, ocmVersion string) *v1alpha1.Component {
	return &v1alpha1.Component{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Component",
			APIVersion: v1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: v1alpha1.ComponentSpec{
			RepositoryRef: v1alpha1.ObjectKey{
				Namespace: namespace,
				Name:      repoName,
			},
			Component: ocmName,
			Semver:    ocmVersion,
			Interval:  metav1.Duration{Duration: reconciliationInterval},
		},
	}
}

func NewTestReplication(namespace, name, compName, targetRepoName string) *v1alpha1.Replication {
	return &v1alpha1.Replication{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Replication",
			APIVersion: v1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ReplicationSpec{
			ComponentRef: v1alpha1.ObjectKey{
				Name:      compName,
				Namespace: namespace,
			},
			TargetRepositoryRef: v1alpha1.ObjectKey{
				Name:      targetRepoName,
				Namespace: namespace,
			},
			Interval: metav1.Duration{Duration: reconciliationInterval},
		},
	}
}

func NewTestComponentInfo(ocmName, ocmVersion string, rawRepoSpec *[]byte) *v1alpha1.ComponentInfo {
	return &v1alpha1.ComponentInfo{
		RepositorySpec: &apiextensionsv1.JSON{Raw: *rawRepoSpec},
		Component:      ocmName,
		Version:        ocmVersion,
	}
}

func NewTestConfigMapForData(namespace, name, data string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Data: map[string]string{
			v1alpha1.OCMConfigKey: data,
		},
	}
}

// SaveToManifest serializes the given API object to a manifest YAML file at the given path.
func SaveToManifest(obj interface{}, path string) error {
	yamlData, err := yaml.Marshal(obj)
	if err != nil {
		return err
	}
	err = os.WriteFile(path, yamlData, 0o600)
	if err != nil {
		return err
	}

	return nil
}

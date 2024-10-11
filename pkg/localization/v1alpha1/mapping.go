package v1alpha1

import (
	"bytes"
	"io"

	yaml "sigs.k8s.io/yaml/goyaml.v2"
)

func Unmarshal(data []byte, into *LocalizationConfig) error {
	return Decode(bytes.NewReader(data), into)
}

func Decode(reader io.Reader, into *LocalizationConfig) error {
	return yaml.NewDecoder(reader).Decode(into)
}

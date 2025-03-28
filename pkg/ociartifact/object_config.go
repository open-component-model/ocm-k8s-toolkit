package ociartifact

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/opencontainers/go-digest"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ObjectConfig is a wrapper around a client.Object that implements the Content interface.
// For serialization it uses the provided runtime.Encoder.
type ObjectConfig struct {
	client.Object `json:",inline"`
	Encoder       runtime.Encoder
}

var _ Content = &ObjectConfig{}

func (in *ObjectConfig) Open() (io.ReadCloser, error) {
	buf, err := in.AsBuf()
	if err != nil {
		return nil, err
	}

	return io.NopCloser(buf), nil
}

func (in *ObjectConfig) AsBuf() (*bytes.Buffer, error) {
	var buf bytes.Buffer

	if err := in.Encoder.Encode(in.Object, &buf); err != nil {
		return nil, err
	}

	return &buf, nil
}

func (in *ObjectConfig) UnpackIntoDirectory(path string) error {
	buf, err := in.AsBuf()
	if err != nil {
		return err
	}

	return os.WriteFile(fmt.Sprintf("%s-%s.yaml", path, in.GetName()), buf.Bytes(), 0o600)
}

func (in *ObjectConfig) GetDigest() (string, error) {
	buf, err := in.AsBuf()
	if err != nil {
		return "", err
	}

	return digest.NewDigestFromBytes(digest.SHA256, buf.Bytes()).String(), err
}

func (in *ObjectConfig) GetRevision() string {
	return fmt.Sprintf("%s/%s in generation %v", in.GetNamespace(), in.GetName(), in.GetGeneration())
}

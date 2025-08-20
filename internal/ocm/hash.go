package ocm

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"ocm.software/ocm/api/ocm/compdesc"

	v1 "k8s.io/api/core/v1"
	ocmctx "ocm.software/ocm/api/ocm"
	ocmv1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
)

var ErrComponentVersionHashMismatch = errors.New("component version hash mismatch")

// CompareCachedAndLiveHashes compares the normalized hashes of a cached component version
// and the corresponding live version in the repository.
//
// It performs the following steps:
//  1. Looks up the live component version from the given repository.
//  2. Computes a normalized hash for both the cached descriptor and the live descriptor
//     using compdesc.JsonNormalisationV3 and SHA-256.
//  3. Compares the two hashes. If they differ, returns ErrComponentVersionHashMismatch.
//  4. If they match, returns a DigestSpec with the hash metadata.
//
// Parameters:
//   - currentComponentVersion: cached component version (from the current session).
//   - liveRepo: repository to look up the live component version.
//   - component: name of the component.
//   - version: version string of the component.
func CompareCachedAndLiveHashes(
	currentComponentVersion ocmctx.ComponentVersionAccess,
	liveRepo ocmctx.Repository,
	component, version string,
) (_ *ocmv1.DigestSpec, err error) {
	normAlgo := compdesc.JsonNormalisationV3
	liveCV, err := liveRepo.LookupComponentVersion(component, version)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup live component version to compare with current state: %w", err)
	}
	defer func() {
		err = errors.Join(err, liveCV.Close())
	}()
	// cached version from session
	_, hash, err := compdesc.NormHash(currentComponentVersion.GetDescriptor(), normAlgo, sha256.New())
	if err != nil {
		return nil, fmt.Errorf("failed to hash cached component version: %w", err)
	}
	_, liveHash, err := compdesc.NormHash(liveCV.GetDescriptor(), normAlgo, sha256.New())
	if err != nil {
		return nil, fmt.Errorf("failed to hash live component version: %w", err)
	}

	if hash != liveHash {
		return nil, fmt.Errorf("%w: %s != %s", ErrComponentVersionHashMismatch, hash, liveHash)
	}

	return &ocmv1.DigestSpec{
		HashAlgorithm:          "sha256",
		NormalisationAlgorithm: normAlgo,
		Value:                  hash,
	}, nil
}

func GetObjectDataHash[T ctrl.Object](obj ...T) (string, error) {
	hasher := func(obj any) (string, error) {
		switch obj := obj.(type) {
		case *v1.Secret:
			return GetSecretMapDataHash(obj)
		case *v1.ConfigMap:
			return GetConfigMapDataHash(obj)
		default:
			return "", fmt.Errorf("unsupported object type for data hash calculation: %T", obj)
		}
	}

	hashes := make([]string, 0, len(obj))
	for _, o := range obj {
		h, err := hasher(o)
		if err != nil {
			return "", err
		}
		hashes = append(hashes, h)
	}
	slices.SortFunc(hashes, cmp.Compare)
	hashes = slices.Concat(hashes)

	var combined strings.Builder
	for i, hash := range hashes {
		if i > 0 {
			combined.WriteString("\x00") // delimiter
		}
		combined.WriteString(hash)
	}

	if len(obj) > 1 {
		h := sha256.Sum256([]byte(combined.String()))

		return hex.EncodeToString(h[:]), nil
	}

	return combined.String(), nil
}

func GetSecretMapDataHash(secret *v1.Secret) (string, error) {
	if secret == nil {
		return "", nil
	}

	return HashMap(secret.Data)
}

func GetConfigMapDataHash(configMap *v1.ConfigMap) (string, error) {
	if configMap == nil {
		return "", nil
	}
	m := make(map[string][]byte, len(configMap.Data)+len(configMap.BinaryData))
	for k, v := range configMap.Data {
		m[k] = []byte(v)
	}
	for k, v := range configMap.BinaryData {
		m[k] = v
	}
	switch len(m) {
	case 0:
		return "", nil
	default:
		return HashMap(m)
	}
}

// HashMap deterministically hashes map data.
func HashMap(data map[string][]byte) (string, error) {
	var raw bytes.Buffer
	for _, k := range slices.Sorted(maps.Keys(data)) {
		raw.WriteString(k)
		raw.WriteByte(0) // delimiter
		raw.Write(data[k])
		raw.WriteByte(0)
	}
	h := sha256.Sum256(raw.Bytes())

	return hex.EncodeToString(h[:]), nil
}

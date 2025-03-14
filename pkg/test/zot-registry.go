package test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/gomega"

	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
)

const (
	timeout = 30 * time.Second
)

func SetupRegistry(binPath, rootDir, address, port string) (*exec.Cmd, *ociartifact.Registry) {
	config := []byte(fmt.Sprintf(`{"storage":{"rootDirectory":"%s"},"http":{"address":"%s","port": "%s"}}`, rootDir, address, port))
	configFile := filepath.Join(rootDir, "config.json")
	err := os.WriteFile(configFile, config, 0o600)
	Expect(err).NotTo(HaveOccurred())

	// Start zot-registry
	zotCmd := exec.Command(binPath, "serve", configFile)
	err = zotCmd.Start()
	Expect(err).NotTo(HaveOccurred(), "Failed to start Zot")

	// Wait for Zot to be ready
	Eventually(func() error {
		url := fmt.Sprintf("http://%s/v2/", net.JoinHostPort(address, port))
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err != nil {
			return err
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("could not connect to Zot")
		}

		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}

		return nil
	}, timeout).Should(Succeed(), "Zot registry did not start in time")

	registry, err := ociartifact.NewRegistry(fmt.Sprintf("%s:%s", address, port))
	Expect(err).NotTo(HaveOccurred())
	registry.PlainHTTP = true

	return zotCmd, registry
}

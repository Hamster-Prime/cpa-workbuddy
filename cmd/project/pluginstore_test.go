package main_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginstore"
)

type storeHTTPFiles map[string]string

func (files storeHTTPFiles) Do(request *http.Request) (*http.Response, error) {
	name, exists := files[request.URL.String()]
	if !exists || request.Method != http.MethodGet {
		return nil, fmt.Errorf("unexpected plugin store request: %s %s", request.Method, request.URL)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header), Request: request,
		Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)),
	}, nil
}

func verifyStoreInstallation(t *testing.T, f fixture, archivePath, version, libraryDigest string) {
	t.Helper()
	const source = "https://raw.githubusercontent.com/ygq-future/cpa-workbuddy/main/registry.json"
	const releaseURL = "https://api.github.com/repos/ygq-future/cpa-workbuddy/releases/latest"
	const downloads = "https://downloads.example/"
	base := filepath.Base(archivePath)
	release, err := json.Marshal(pluginstore.Release{
		TagName: "v" + version,
		Assets: []pluginstore.ReleaseAsset{
			{Name: base, BrowserDownloadURL: downloads + base},
			{Name: "checksums.txt", BrowserDownloadURL: downloads + "checksums.txt"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	releasePath := filepath.Join(f.root, "out", "store-release.json")
	writeFixtureFile(t, releasePath, string(release))
	client := pluginstore.NewClient(storeHTTPFiles{
		source: filepath.Join(repositoryRoot, "registry.json"), releaseURL: releasePath,
		downloads + base: archivePath, downloads + "checksums.txt": archivePath + ".sha256",
	}, source)
	registry, err := client.FetchRegistry(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Plugins) != 1 || registry.Plugins[0].ID != "cpa-workbuddy" {
		t.Fatalf("unexpected plugin store registry: %+v", registry)
	}
	result, err := client.Install(context.Background(), registry.Plugins[0], pluginstore.InstallOptions{
		PluginsDir: filepath.Join(f.root, "out", "store-plugins"), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	})
	if err != nil {
		t.Fatalf("official CPA store cannot install release package: %v", err)
	}
	extension := map[string]string{"windows": ".dll", "linux": ".so", "darwin": ".dylib"}[runtime.GOOS]
	if result.Version != version || result.ReleaseTag != "v"+version || filepath.Base(result.Path) != "cpa-workbuddy-v"+version+extension {
		t.Fatalf("unexpected installed plugin identity: %+v", result)
	}
	if digest := fmt.Sprintf("%x", sha256.Sum256(readFixtureFile(t, result.Path))); digest != libraryDigest {
		t.Fatal("plugin store installed different library bytes")
	}
}

package main_test

import (
	"archive/zip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestPackageContentsAndChecksums(t *testing.T) {
	f := newFixture(t)
	for _, relative := range []string{"version.go", "LICENSE", "README.md"} {
		copyFixtureFile(t, filepath.Join(repositoryRoot, relative), filepath.Join(f.root, relative))
	}
	for _, relative := range []string{"cmd/cpa-workbuddy", "internal", "docs"} {
		copyFixtureTree(t, filepath.Join(repositoryRoot, filepath.FromSlash(relative)), filepath.Join(f.root, filepath.FromSlash(relative)))
	}
	for _, relative := range []string{"auth/account.json", "out/logs/private.log", "out/scratch/download.zip", "_ref/private.txt", "docs/credentials.json"} {
		writeFixtureFile(t, filepath.Join(f.root, filepath.FromSlash(relative)), "private-package-fixture")
	}
	requireSuccess(t, f.execute(t, "package"))
	version := strings.TrimSpace(string(readFixtureFile(t, filepath.Join(f.root, "VERSION"))))
	base := fmt.Sprintf("cpa-workbuddy-v%s-%s-%s.zip", version, runtime.GOOS, runtime.GOARCH)
	archivePath := filepath.Join(f.root, "out", "dist", base)
	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	archiveHash := sha256.New()
	_, err = io.Copy(archiveHash, archive)
	if closeErr := archive.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(readFixtureFile(t, archivePath+".sha256")), fmt.Sprintf("%x  %s\n", archiveHash.Sum(nil), base); got != want {
		t.Fatalf("archive checksum mismatch: got %q, want %q", got, want)
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	})
	extension := map[string]string{"windows": ".dll", "linux": ".so", "darwin": ".dylib"}[runtime.GOOS]
	expected := map[string]bool{}
	for _, name := range []string{
		"cpa-workbuddy" + extension, "cpa-workbuddy.h", "native.h", "VERSION", "LICENSE", "README.md",
		"licenses/Go-LICENSE", "licenses/Go-PATENTS", "docs/licenses/CLIProxyAPI-MIT.txt",
		"docs/patches/cpa-v7.3.15.patch", "docs/requirements.md", "docs/architecture.md",
		"docs/compatibility.md", "docs/quality.md", "docs/deployment.md", "docs/third-party-notices.md",
		"docs/tasks/workbuddy-plugin.md", "build-info.txt", "checksums.sha256",
	} {
		expected[name] = true
	}
	actual := map[string]string{}
	text := map[string]string{}
	for _, member := range reader.File {
		if !expected[member.Name] {
			t.Fatalf("unapproved package member: %q", member.Name)
		}
		if _, exists := actual[member.Name]; exists {
			t.Fatalf("duplicate package member: %q", member.Name)
		}
		stream, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.New()
		if member.Name == "VERSION" || member.Name == "build-info.txt" || member.Name == "checksums.sha256" || strings.HasSuffix(member.Name, ".md") {
			data, readErr := io.ReadAll(io.TeeReader(stream, digest))
			text[member.Name] = string(data)
			err = readErr
		} else {
			_, err = io.Copy(digest, stream)
		}
		if closeErr := stream.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err != nil {
			t.Fatal(err)
		}
		actual[member.Name] = fmt.Sprintf("%x", digest.Sum(nil))
	}
	for name := range expected {
		if _, exists := actual[name]; !exists {
			t.Errorf("missing package member: %q", name)
		}
	}
	verified := map[string]bool{}
	for line := range strings.SplitSeq(strings.TrimSpace(text["checksums.sha256"]), "\n") {
		digest, name, ok := strings.Cut(line, "  ")
		if !ok || name == "checksums.sha256" || verified[name] || actual[name] != digest {
			t.Fatalf("invalid member checksum: %q", line)
		}
		verified[name] = true
	}
	for name := range actual {
		if name != "checksums.sha256" && !verified[name] {
			t.Errorf("package member has no checksum: %q", name)
		}
	}
	if strings.TrimSpace(text["VERSION"]) != version {
		t.Fatal("packaged version differs from root VERSION")
	}
	info := text["build-info.txt"]
	for _, field := range []string{"product\tcpa-workbuddy", "version\t" + version, "GOOS=" + runtime.GOOS, "GOARCH=" + runtime.GOARCH, "CGO_ENABLED=1", "-buildmode=c-shared"} {
		if !strings.Contains(info, field) {
			t.Errorf("native build metadata missing %q: %s", field, info)
		}
	}
	if strings.Contains(info, f.root) {
		t.Fatal("native build metadata contains the fixture's local path")
	}
	goEnvironment := f.run(t, "go", "env", "GOROOT")
	requireSuccess(t, goEnvironment)
	toolchainRoot := strings.TrimSpace(goEnvironment.stdout)
	for name, source := range map[string]string{
		"licenses/Go-LICENSE":               filepath.Join(toolchainRoot, "LICENSE"),
		"licenses/Go-PATENTS":               filepath.Join(toolchainRoot, "PATENTS"),
		"docs/licenses/CLIProxyAPI-MIT.txt": filepath.Join(repositoryRoot, "docs", "licenses", "CLIProxyAPI-MIT.txt"),
	} {
		if want := fmt.Sprintf("%x", sha256.Sum256(readFixtureFile(t, source))); actual[name] != want {
			t.Errorf("license text changed in %q", name)
		}
	}
	links := regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	for name, contents := range text {
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		for _, match := range links.FindAllStringSubmatch(contents, -1) {
			target, err := url.Parse(match[1])
			if err != nil {
				t.Fatalf("invalid package document link in %s: %v", name, err)
			}
			if target.IsAbs() || target.Path == "" {
				continue
			}
			destination := path.Join(path.Dir(name), target.Path)
			if _, exists := actual[destination]; !exists {
				t.Errorf("package document %s links to missing member %s", name, destination)
			}
		}
	}
}

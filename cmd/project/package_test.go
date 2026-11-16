package main_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
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
	for _, relative := range []string{"go.mod", "go.sum", "version.go", "LICENSE", "README.md"} {
		copyFixtureFile(t, filepath.Join(repositoryRoot, relative), filepath.Join(f.root, relative))
	}
	for _, relative := range []string{"cmd/cpa-workbuddy", "internal", "docs"} {
		copyFixtureTree(t, filepath.Join(repositoryRoot, filepath.FromSlash(relative)), filepath.Join(f.root, filepath.FromSlash(relative)))
	}
	for _, relative := range []string{"auth/account.json", "out/logs/private.log", "out/scratch/download.zip", "_ref/private.txt", "docs/credentials.json", "docs/local/sample.patch"} {
		writeFixtureFile(t, filepath.Join(f.root, filepath.FromSlash(relative)), "private-package-fixture")
	}
	copyFixtureChecksumState(t, f.root)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	licenseCache := filepath.Join("out", "cache", "licenses")
	copyFixtureTree(t, filepath.Join(repositoryRoot, licenseCache), filepath.Join(f.root, licenseCache))
	moduleFiles := map[string][]byte{}
	for _, name := range []string{"go.mod", "go.sum"} {
		moduleFiles[name] = readFixtureFile(t, filepath.Join(f.root, name))
	}
	requireSuccess(t, f.execute(t, "quality", "--stage", "modules"))
	requireSuccess(t, f.execute(t, "package"))
	for name, expected := range moduleFiles {
		if !bytes.Equal(readFixtureFile(t, filepath.Join(f.root, name)), expected) {
			t.Fatalf("modules and package modified source manifest %s", name)
		}
	}
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
		"licenses/Go-LICENSE", "licenses/Go-PATENTS", "licenses/THIRD-PARTY.txt",
		"docs/requirements.md", "docs/architecture.md", "docs/compatibility.md", "docs/quality.md", "docs/deployment.md",
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
		if member.Name == "VERSION" || member.Name == "build-info.txt" || member.Name == "checksums.sha256" || strings.HasSuffix(member.Name, ".md") || strings.HasSuffix(member.Name, ".txt") {
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
	for _, field := range []string{
		"dep\tgithub.com/router-for-me/CLIProxyAPI/v8\tv8.0.69\t",
		"dep\tgithub.com/MoeForge/moego\tv0.1.0-alpha.1\n",
		"=>\tgithub.com/router-for-me/moego\tv0.1.2\t",
	} {
		if !strings.Contains(info, field) {
			t.Errorf("native build metadata missing production dependency %q", field)
		}
	}
	if strings.Contains(info, f.root) {
		t.Fatal("native build metadata contains the fixture's local path")
	}
	goEnvironment := f.run(t, "go", "env", "GOROOT")
	requireSuccess(t, goEnvironment)
	toolchainRoot := strings.TrimSpace(goEnvironment.stdout)
	for name, source := range map[string]string{
		"licenses/Go-LICENSE": filepath.Join(toolchainRoot, "LICENSE"),
		"licenses/Go-PATENTS": filepath.Join(toolchainRoot, "PATENTS"),
	} {
		if want := fmt.Sprintf("%x", sha256.Sum256(readFixtureFile(t, source))); actual[name] != want {
			t.Errorf("license text changed in %q", name)
		}
	}
	verifyDependencyLicenses(t, f, text["licenses/THIRD-PARTY.txt"], extension)
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
	cachePath := filepath.Join(f.root, licenseCache, "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30")
	cacheBytes := readFixtureFile(t, cachePath)
	writeFixtureFile(t, cachePath, "corrupt license material")
	requireFailure(t, f.execute(t, "package"), "does not match SHA256")
	writeFixtureFile(t, cachePath, string(cacheBytes))
	if err := os.Remove(cachePath); err != nil {
		t.Fatal(err)
	}
	requireFailure(t, f.execute(t, "package"), "run go run ./cmd/project bootstrap")
}

func verifyDependencyLicenses(t *testing.T, f fixture, notices, extension string) {
	t.Helper()
	expected := map[string][]string{
		"github.com/cespare/xxhash/v2@v2.3.0":                                  {"LICENSE.txt"},
		"github.com/gabriel-vasile/mimetype@v1.4.3":                            {"LICENSE"},
		"github.com/gin-contrib/sse@v0.1.0":                                    {"LICENSE"},
		"github.com/gin-gonic/gin@v1.10.1":                                     {"LICENSE"},
		"github.com/go-playground/locales@v0.14.1":                             {"LICENSE"},
		"github.com/go-playground/universal-translator@v0.18.1":                {"LICENSE"},
		"github.com/go-playground/validator/v10@v10.20.0":                      {"LICENSE"},
		"github.com/google/uuid@v1.6.0":                                        {"LICENSE"},
		"github.com/leodido/go-urn@v1.4.0":                                     {"LICENSE"},
		"github.com/mattn/go-isatty@v0.0.20":                                   {"LICENSE"},
		"github.com/pelletier/go-toml/v2@v2.4.3-0.20260624044523-35f78d5908f1": {"LICENSE"},
		"github.com/redis/go-redis/v9@v9.19.0":                                 {"LICENSE"},
		"github.com/router-for-me/CLIProxyAPI/v8@v8.0.69":                      {"LICENSE"},
		"github.com/router-for-me/moego@v0.1.2":                                {"LICENSE"},
		"github.com/sirupsen/logrus@v1.9.3":                                    {"LICENSE"},
		"github.com/ugorji/go/codec@v1.2.12":                                   {"LICENSE"},
		"go.etcd.io/bbolt@v1.5.0":                                              {"LICENSE"},
		"go.uber.org/atomic@v1.11.0":                                           {"LICENSE.txt"},
		"golang.org/x/crypto@v0.57.0":                                          {"LICENSE", "PATENTS"},
		"golang.org/x/net@v0.60.0":                                             {"LICENSE", "PATENTS"},
		"golang.org/x/sys@v0.48.0":                                             {"LICENSE", "PATENTS"},
		"golang.org/x/text@v0.42.0":                                            {"LICENSE", "PATENTS"},
		"google.golang.org/protobuf@v1.34.1":                                   {"LICENSE", "PATENTS"},
		"gopkg.in/yaml.v3@v3.0.1":                                              {"LICENSE", "NOTICE"},
	}
	type module struct {
		Path    string
		Version string
		Dir     string
		Sum     string
		Replace *module
	}
	metadata := f.run(t, "go", "list", "-deps", "-json", "./cmd/cpa-workbuddy")
	requireSuccess(t, metadata)
	modules := map[string]module{}
	decoder := json.NewDecoder(strings.NewReader(metadata.stdout))
	for {
		var item struct {
			Module *module
		}
		if err := decoder.Decode(&item); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if item.Module == nil {
			continue
		}
		source := *item.Module
		if source.Replace != nil {
			source = *source.Replace
		}
		modules[source.Path+"@"+source.Version] = source
	}
	native, err := buildinfo.ReadFile(filepath.Join(f.root, "out", "bin", "cpa-workbuddy"+extension))
	if err != nil {
		t.Fatal(err)
	}
	if len(native.Deps) != len(expected) || strings.Count(notices, "\nModule: ") != len(expected) {
		t.Fatalf("linked modules and license inventory must match the %d reviewed dependencies", len(expected))
	}
	for _, dependency := range native.Deps {
		if dependency.Replace != nil {
			dependency = dependency.Replace
		}
		identity := dependency.Path + "@" + dependency.Version
		files, exists := expected[identity]
		if !exists {
			t.Fatalf("unreviewed linked module %s", identity)
		}
		source, exists := modules[identity]
		if !exists || source.Dir == "" {
			t.Fatalf("missing Go download source for %s", identity)
		}
		_, section, exists := strings.Cut(notices, "\nModule: "+identity+"\n")
		if !exists {
			t.Fatalf("missing license notice for linked module %s", identity)
		}
		section, _, _ = strings.Cut(section, "\n==============================================================================\n")
		if !strings.Contains(section, "Go module sum: "+dependency.Sum+"\n") || source.Sum != dependency.Sum {
			t.Errorf("license notice module sum differs from linked dependency %s", identity)
		}
		for _, name := range files {
			original := readFixtureFile(t, filepath.Join(source.Dir, name))
			block := fmt.Sprintf("File: %s\nSHA256: %x\n[BEGIN %s]\n%s\n[END %s]\n", name, sha256.Sum256(original), name, original, name)
			if !bytes.Contains([]byte(section), []byte(block)) {
				t.Errorf("license notice differs from downloaded %s/%s", identity, name)
			}
		}
	}
	for _, notice := range []string{
		"Source notice: internal/json/json.go\n",
		"Source notice: path.go\n",
		"Source notice: tree.go\n",
		"Source notice: alt_exit.go\n",
		"Source notice: internal/once.go\n",
		"Source notice: internal/util/atomic_max.go\n",
		"Source notice: internal/util/atomic_min.go\n",
		"Source notice: internal/hashtag/hashtag.go\n",
		"Embedded source: github.com/julienschmidt/httprouter@v1.3.0\n",
		"SHA256: 3d41422b23782d18b02880f557594dab7ffc8a192167ff11ebdcbe26817ea8b2\n",
		"SHA256: 9cf8c936402ee68414f4b77b39ede89d925465b4c8477a0730480ae0dd45f3a0\n",
	} {
		if !strings.Contains(notices, notice) {
			t.Errorf("missing embedded source attribution %q", notice)
		}
	}
}

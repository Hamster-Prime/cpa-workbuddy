package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	dependencySeparator = "==============================================================================\n"
	licenseFetchTimeout = 30 * time.Second
	licenseFetchLimit   = 1 << 20

	apacheLicenseDigest     = "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"
	httprouterLicenseDigest = "162ce11ad71338d0a0c6ebaf5c48af72c6ae237b468859d3656fe2d9ed3f3a85"
	parlLicenseDigest       = "3d41422b23782d18b02880f557594dab7ffc8a192167ff11ebdcbe26817ea8b2"
	crc16LicenseDigest      = "9cf8c936402ee68414f4b77b39ede89d925465b4c8477a0730480ae0dd45f3a0"
)

var moduleLicenseNames = []string{"LICENSE", "LICENSE.txt", "NOTICE", "PATENTS"}

var licenseNoticeMarkers = [][]byte{
	[]byte("Copyright"),
	[]byte("copyright notice"),
	[]byte("SPDX-License-Identifier"),
	[]byte("Licensed under"),
	[]byte("Permission is hereby granted"),
	[]byte("Permission to use, copy, modify"),
	[]byte("Redistribution and use in source and binary forms"),
	[]byte("All rights reserved"),
	[]byte("ISC License"),
	[]byte("©"),
}

type listedModule struct {
	Path    string
	Version string
	Dir     string
	Sum     string
	Main    bool
	Replace *listedModule
}

type listedPackage struct {
	Dir      string
	GoFiles  []string
	CgoFiles []string
	Module   *listedModule
}

type dependencyModule struct {
	path            string
	version         string
	requiredPath    string
	requiredVersion string
	dir             string
	sum             string
	files           []string
}

type dependencyLicense struct {
	name string
	data []byte
}

type dependencySourceNotice struct {
	files []string
	text  string
}

type dependencyNotice struct {
	*dependencyModule
	licenses []dependencyLicense
	notices  []dependencySourceNotice
}

type embeddedLicense struct {
	heading     string
	usedBy      string
	modulePaths []string
	noticeFiles []string
	source      string
	file        string
	digest      string
	endMarker   string
	text        []byte
}

// embeddedLicenseSources are the texts that cannot be recovered from a module
// root. Each entry is tied to the modules whose compiled sources embed it.
var embeddedLicenseSources = []embeddedLicense{
	{
		heading:     "github.com/julienschmidt/httprouter@v1.3.0",
		modulePaths: []string{"github.com/gin-gonic/gin"},
		noticeFiles: []string{"path.go", "tree.go"},
		source:      "https://raw.githubusercontent.com/julienschmidt/httprouter/v1.3.0/LICENSE",
		file:        "LICENSE",
		digest:      httprouterLicenseDigest,
	},
	{
		heading:     "haraldrudell/parl ISC terms",
		modulePaths: []string{"github.com/redis/go-redis/v9"},
		noticeFiles: []string{"internal/util/atomic_max.go", "internal/util/atomic_min.go"},
		source:      "https://raw.githubusercontent.com/haraldrudell/parl/main/LICENSE",
		file:        "parl-LICENSE",
		digest:      parlLicenseDigest,
	},
	{
		heading:     "Georges Menie CRC16 BSD-3-Clause terms",
		modulePaths: []string{"github.com/redis/go-redis/v9"},
		noticeFiles: []string{"internal/hashtag/hashtag.go"},
		source:      "https://www.menie.org/georges/embedded/crc16.c",
		file:        "crc16.c license notice",
		digest:      crc16LicenseDigest,
		endMarker:   "#include",
	},
	{
		heading:     "Apache License 2.0",
		modulePaths: []string{"gopkg.in/yaml.v3", "github.com/redis/go-redis/v9"},
		source:      "https://www.apache.org/licenses/LICENSE-2.0.txt",
		file:        "Apache-2.0.txt",
		digest:      apacheLicenseDigest,
	},
}

// dependencyNotices builds the third-party inventory for the modules linked into
// the built artifact. Module sources come from go list, while versions and sums
// are checked against the artifact's own build information so a stale or edited
// inventory cannot silently ship.
func (c *controller) dependencyNotices(info *buildinfo.BuildInfo) ([]byte, error) {
	output, err := c.tool("go", runPolicy{stderrFails: true}, "list", "-deps", "-json", "./cmd/cpa-workbuddy")
	if err != nil {
		return nil, err
	}
	modules, err := parseDependencyPackages(output)
	if err != nil {
		return nil, err
	}
	dependencies, err := dependencyNoticeSources(info, modules)
	if err != nil {
		return nil, err
	}
	embedded, err := c.embeddedLicenses(dependencies)
	if err != nil {
		return nil, err
	}
	return renderDependencyNotices(dependencies, embedded), nil
}

func parseDependencyPackages(output []byte) (map[string]*dependencyModule, error) {
	modules := make(map[string]*dependencyModule)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var pkg listedPackage
		err := decoder.Decode(&pkg)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode go list dependency metadata: %w", err)
		}
		if pkg.Module == nil || pkg.Module.Main {
			continue
		}
		source := pkg.Module
		for source.Replace != nil {
			source = source.Replace
		}
		if source.Path == "" || source.Version == "" {
			return nil, errors.New("go list reported a module without a path or version")
		}
		if source.Dir == "" {
			return nil, fmt.Errorf("go list reported no source directory for %s@%s", source.Path, source.Version)
		}
		key := dependencyIdentity(source.Path, source.Version)
		module := modules[key]
		if module == nil {
			module = &dependencyModule{
				path:            source.Path,
				version:         source.Version,
				requiredPath:    pkg.Module.Path,
				requiredVersion: pkg.Module.Version,
				dir:             source.Dir,
				sum:             source.Sum,
			}
			modules[key] = module
		} else {
			if module.dir != source.Dir {
				return nil, fmt.Errorf("go list reported inconsistent directories for %s", key)
			}
			if module.sum != source.Sum {
				return nil, fmt.Errorf("go list reported inconsistent sums for %s", key)
			}
			if module.requiredPath != pkg.Module.Path || module.requiredVersion != pkg.Module.Version {
				return nil, fmt.Errorf("go list reported inconsistent module identities for %s", key)
			}
		}
		if pkg.Dir == "" {
			return nil, fmt.Errorf("go list reported no package directory for %s", key)
		}
		for _, names := range [][]string{pkg.GoFiles, pkg.CgoFiles} {
			for _, name := range names {
				file := filepath.Join(pkg.Dir, name)
				module.files = append(module.files, file)
			}
		}
	}
	for _, module := range modules {
		sort.Strings(module.files)
	}
	return modules, nil
}

// dependencyNoticeSources validates every linked dependency against the go list
// metadata and collects original root license bytes and license-bearing source
// comments.
func dependencyNoticeSources(info *buildinfo.BuildInfo, modules map[string]*dependencyModule) ([]dependencyNotice, error) {
	if info == nil {
		return nil, errors.New("missing build info for dependency notices")
	}
	seen := make(map[string]bool)
	var result []dependencyNotice
	for _, dependency := range info.Deps {
		if dependency == nil {
			return nil, errors.New("build info contains a nil dependency")
		}
		source := dependency
		for source.Replace != nil {
			source = source.Replace
		}
		if source.Path == "" || source.Version == "" {
			return nil, fmt.Errorf("dependency %q has no path or version in build info", dependency.Path)
		}
		key := dependencyIdentity(source.Path, source.Version)
		if seen[key] {
			continue
		}
		seen[key] = true
		module := modules[key]
		if module == nil {
			return nil, fmt.Errorf("go list did not report linked module %s", key)
		}
		if module.path != source.Path || module.version != source.Version {
			return nil, fmt.Errorf("module %s differs from go list module %s@%s", key, module.path, module.version)
		}
		if module.requiredPath != dependency.Path || module.requiredVersion != dependency.Version {
			return nil, fmt.Errorf("module %s is required as %s@%s but go list reports %s@%s", key, dependency.Path, dependency.Version, module.requiredPath, module.requiredVersion)
		}
		if source.Sum == "" {
			return nil, fmt.Errorf("module %s has no sum in build info", key)
		}
		if module.sum == "" || module.sum != source.Sum {
			return nil, fmt.Errorf("module sum mismatch for %s: build info %s, go list %s", key, source.Sum, module.sum)
		}
		licenses, err := readModuleLicenses(key, module.dir)
		if err != nil {
			return nil, err
		}
		notices, err := collectSourceNotices(key, module.dir, module.files)
		if err != nil {
			return nil, err
		}
		result = append(result, dependencyNotice{
			dependencyModule: module,
			licenses:         licenses,
			notices:          notices,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return dependencyIdentity(result[left].path, result[left].version) < dependencyIdentity(result[right].path, result[right].version)
	})
	return result, nil
}

func readModuleLicenses(identity, directory string) ([]dependencyLicense, error) {
	if directory == "" {
		return nil, fmt.Errorf("module %s has no source directory", identity)
	}
	var licenses []dependencyLicense
	hasLicense := false
	for _, name := range moduleLicenseNames {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s license file %s: %w", identity, name, err)
		}
		hasLicense = hasLicense || name == "LICENSE" || name == "LICENSE.txt"
		licenses = append(licenses, dependencyLicense{name: name, data: data})
	}
	if !hasLicense {
		return nil, fmt.Errorf("module %s has no LICENSE or LICENSE.txt file", identity)
	}
	return licenses, nil
}

func collectSourceNotices(identity, directory string, files []string) ([]dependencySourceNotice, error) {
	textFiles := make(map[string]map[string]bool)
	for _, file := range files {
		relative, err := filepath.Rel(directory, file)
		if err != nil {
			return nil, fmt.Errorf("locate %s source file for %s: %w", identity, file, err)
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("source file %s is outside module %s", file, identity)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s source file %s: %w", identity, file, err)
		}
		if !containsLicenseNotice(data) {
			continue
		}
		position := token.NewFileSet()
		parsed, err := parser.ParseFile(position, file, data, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s source file %s: %w", identity, file, err)
		}
		var blocks []string
		for _, group := range parsed.Comments {
			if !containsLicenseNotice([]byte(group.Text())) {
				continue
			}
			start := position.Position(group.Pos()).Offset
			end := position.Position(group.End()).Offset
			if start < 0 || end > len(data) || start >= end {
				return nil, fmt.Errorf("invalid comment position in %s source file %s", identity, file)
			}
			text := strings.TrimRight(string(data[start:end]), " \t\r\n")
			if strings.TrimSpace(text) == "" {
				continue
			}
			blocks = append(blocks, text)
		}
		if len(blocks) == 0 {
			continue
		}
		text := strings.Join(blocks, "\n\n")
		if textFiles[text] == nil {
			textFiles[text] = make(map[string]bool)
		}
		textFiles[text][filepath.ToSlash(relative)] = true
	}
	notices := make([]dependencySourceNotice, 0, len(textFiles))
	for text, files := range textFiles {
		names := make([]string, 0, len(files))
		for name := range files {
			names = append(names, name)
		}
		sort.Strings(names)
		notices = append(notices, dependencySourceNotice{files: names, text: text})
	}
	sort.Slice(notices, func(left, right int) bool {
		if notices[left].files[0] != notices[right].files[0] {
			return notices[left].files[0] < notices[right].files[0]
		}
		return notices[left].text < notices[right].text
	})
	return notices, nil
}

func containsLicenseNotice(data []byte) bool {
	for _, marker := range licenseNoticeMarkers {
		if bytes.Contains(data, marker) {
			return true
		}
	}
	return false
}

func (c *controller) embeddedLicenses(dependencies []dependencyNotice) ([]embeddedLicense, error) {
	present := make(map[string]string)
	for _, dependency := range dependencies {
		present[dependency.path] = dependencyIdentity(dependency.path, dependency.version)
	}
	cacheDirectory := filepath.Join(c.out, "cache", "licenses")
	client := &http.Client{Timeout: licenseFetchTimeout}
	var result []embeddedLicense
	for _, item := range embeddedLicenseSources {
		var usedBy []string
		for _, modulePath := range item.modulePaths {
			identity, ok := present[modulePath]
			if !ok {
				continue
			}
			usedBy = append(usedBy, usedByWithFiles(identity, licenseNoticeFiles(dependencies, modulePath, item.noticeFiles...)))
		}
		if len(usedBy) == 0 {
			continue
		}
		text, err := loadLicenseText(cacheDirectory, item, client, false)
		if err != nil {
			return nil, fmt.Errorf("embedded license %s: %w", item.heading, err)
		}
		item.usedBy = strings.Join(usedBy, ", ")
		item.text = text
		result = append(result, item)
	}
	return result, nil
}

// prepareLicenseSources primes the offline cache for the supplemental texts
// required by the linked modules. It only downloads missing entries and never
// replaces corrupt cache contents.
func (c *controller) prepareLicenseSources() error {
	cacheDirectory := filepath.Join(c.out, "cache", "licenses")
	client := &http.Client{Timeout: licenseFetchTimeout}
	for _, item := range embeddedLicenseSources {
		if _, err := loadLicenseText(cacheDirectory, item, client, true); err != nil {
			return fmt.Errorf("prepare embedded license %s: %w", item.heading, err)
		}
	}
	return nil
}

func licenseNoticeFiles(dependencies []dependencyNotice, modulePath string, names ...string) []string {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	seen := make(map[string]bool)
	var files []string
	for _, dependency := range dependencies {
		if dependency.path != modulePath {
			continue
		}
		for _, notice := range dependency.notices {
			for _, file := range notice.files {
				if wanted[file] && !seen[file] {
					seen[file] = true
					files = append(files, file)
				}
			}
		}
	}
	sort.Strings(files)
	return files
}

func usedByWithFiles(identity string, files []string) string {
	if len(files) == 0 {
		return identity
	}
	return identity + " (" + strings.Join(files, ", ") + ")"
}

func renderDependencyNotices(dependencies []dependencyNotice, embedded []embeddedLicense) []byte {
	var output bytes.Buffer
	output.WriteString("Third-party Go module licenses and source notices\n\n")
	output.WriteString("Generated from go list -deps -json ./cmd/cpa-workbuddy and the linked build info.\n")
	output.WriteString("Modules, files, and notices are sorted; original license and notice bytes are\n")
	output.WriteString("retained between BEGIN/END delimiters. Module sums are from the Go module\n")
	output.WriteString("download cache. Supplemental texts are verified against fixed SHA256 digests\n")
	output.WriteString("and cached under out/cache/licenses.\n")
	for _, dependency := range dependencies {
		output.WriteString("\n")
		output.WriteString(dependencySeparator)
		fmt.Fprintf(&output, "Module: %s@%s\n", dependency.path, dependency.version)
		fmt.Fprintf(&output, "Required as: %s@%s\n", dependency.requiredPath, dependency.requiredVersion)
		fmt.Fprintf(&output, "Go module sum: %s\n", dependency.sum)
		fmt.Fprintf(&output, "Source: %s\n", moduleZipURL(dependency.path, dependency.version))
		for _, license := range dependency.licenses {
			fmt.Fprintf(&output, "File: %s\nSHA256: %x\n[BEGIN %s]\n", license.name, sha256.Sum256(license.data), license.name)
			writeNoticeBytes(&output, license.data)
			fmt.Fprintf(&output, "[END %s]\n", license.name)
		}
		for _, notice := range dependency.notices {
			fmt.Fprintf(&output, "Source notice: %s\n", strings.Join(notice.files, ", "))
			writeNoticeBytes(&output, []byte(notice.text))
		}
	}
	for _, license := range embedded {
		output.WriteString("\n")
		output.WriteString(dependencySeparator)
		fmt.Fprintf(&output, "Embedded source: %s\n", license.heading)
		fmt.Fprintf(&output, "Used by: %s\n", license.usedBy)
		fmt.Fprintf(&output, "Source: %s\n", license.source)
		fmt.Fprintf(&output, "File: %s\nSHA256: %x\n[BEGIN %s]\n", license.file, sha256.Sum256(license.text), license.file)
		writeNoticeBytes(&output, license.text)
		fmt.Fprintf(&output, "[END %s]\n", license.file)
	}
	return output.Bytes()
}

func writeNoticeBytes(output *bytes.Buffer, data []byte) {
	output.Write(data)
	output.WriteByte('\n')
}

func moduleZipURL(modulePath, version string) string {
	return "https://proxy.golang.org/" + escapeModuleComponent(modulePath) + "/@v/" + escapeModuleComponent(version) + ".zip"
}

func escapeModuleComponent(value string) string {
	var escaped strings.Builder
	escaped.Grow(len(value))
	for _, character := range value {
		if character >= 'A' && character <= 'Z' {
			escaped.WriteByte('!')
			escaped.WriteRune(character + ('a' - 'A'))
			continue
		}
		escaped.WriteRune(character)
	}
	return escaped.String()
}

func dependencyIdentity(modulePath, version string) string {
	return modulePath + "@" + version
}

// loadLicenseText verifies the pinned bytes on every read. Bootstrap may fetch
// missing material; packaging consumes the verified local cache.
func loadLicenseText(cacheDirectory string, source embeddedLicense, client *http.Client, fetch bool) ([]byte, error) {
	digest := source.digest
	want, err := decodeLicenseDigest(digest)
	if err != nil {
		return nil, err
	}
	name := filepath.Join(cacheDirectory, strings.ToLower(digest))
	data, err := os.ReadFile(name)
	switch {
	case err == nil:
		if sha256.Sum256(data) != want {
			return nil, fmt.Errorf("cached license %s does not match SHA256 %s", name, digest)
		}
		return data, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("read cached license %s: %w", name, err)
	}
	if !fetch {
		return nil, fmt.Errorf("missing cached license %s; run go run ./cmd/project bootstrap", digest)
	}
	data, err = downloadLicense(source, want, client)
	if err != nil {
		return nil, err
	}
	if err := cacheLicense(cacheDirectory, strings.ToLower(digest), data); err != nil {
		return nil, err
	}
	return data, nil
}

func decodeLicenseDigest(value string) ([sha256.Size]byte, error) {
	var summary [sha256.Size]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return summary, fmt.Errorf("invalid SHA256 digest %q", value)
	}
	copy(summary[:], decoded)
	return summary, nil
}

func downloadLicense(source embeddedLicense, want [sha256.Size]byte, client *http.Client) (data []byte, resultErr error) {
	sourceURL := source.source
	ctx, cancel := context.WithTimeout(context.Background(), licenseFetchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("request license %s: %w", sourceURL, err)
	}
	if client == nil {
		client = &http.Client{Timeout: licenseFetchTimeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download license %s: %w", sourceURL, err)
	}
	defer func() { resultErr = errors.Join(resultErr, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download license %s: unexpected status %s", sourceURL, response.Status)
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, licenseFetchLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read license %s: %w", sourceURL, err)
	}
	if len(data) > licenseFetchLimit {
		return nil, fmt.Errorf("license %s exceeds %d bytes", sourceURL, licenseFetchLimit)
	}
	if source.endMarker != "" {
		end := bytes.Index(data, []byte(source.endMarker))
		if end < 0 {
			return nil, fmt.Errorf("license header boundary missing in %s", sourceURL)
		}
		data = data[:end]
	}
	if sha256.Sum256(data) != want {
		return nil, fmt.Errorf("downloaded license %s does not match SHA256 %x", sourceURL, want)
	}
	return data, nil
}

func cacheLicense(directory, digest string, data []byte) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create license cache %s: %w", directory, err)
	}
	temporary, err := os.CreateTemp(directory, ".license-*")
	if err != nil {
		return fmt.Errorf("create license cache entry in %s: %w", directory, err)
	}
	name := temporary.Name()
	_, writeErr := temporary.Write(data)
	if err := errors.Join(writeErr, temporary.Close()); err != nil {
		return errors.Join(fmt.Errorf("write license cache entry %s: %w", name, err), os.Remove(name))
	}
	if err := os.Rename(name, filepath.Join(directory, digest)); err != nil {
		return errors.Join(fmt.Errorf("commit license cache entry %s: %w", name, err), os.Remove(name))
	}
	return nil
}

// Command project runs repository lifecycle tasks and check-only quality gates.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	goVersion        = "go1.27.1"
	goplsVersion     = "v0.23.0"
	golangciVersion  = "2.12.2"
	semverNumber     = `(0|[1-9][0-9]*)`
	semverIdentifier = `(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)`
)

var (
	stages        = []string{"tools", "upstream", "version", "format", "modules", "vet", "lsp", "lint", "build", "test"}
	semverPattern = regexp.MustCompile(`^` + semverNumber + `\.` + semverNumber + `\.` + semverNumber + `(-` + semverIdentifier + `(\.` + semverIdentifier + `)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	commitPattern = regexp.MustCompile(`^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([a-z0-9][a-z0-9_./-]*\))?!?: [^\s].*$`)
	mergePattern  = regexp.MustCompile(`^Merge (branch |remote-tracking branch |pull request |tag ).+$`)
	revertPattern = regexp.MustCompile(`^Revert ".+"$`)
)

type options struct {
	command     string
	stages      repeatedFlag
	tag         string
	messageFile string
	commitRange string
	staged      bool
	githubEvent bool
	verbose     bool
}

type repeatedFlag []string

func (value *repeatedFlag) String() string { return strings.Join(*value, ",") }

func (value *repeatedFlag) Set(item string) error {
	*value = append(*value, item)
	return nil
}

type controller struct {
	root    string
	out     string
	tools   string
	env     []string
	verbose bool
}

type runPolicy struct {
	nonemptyFails bool
	stderrFails   bool
}

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func parseOptions(args []string) (options, error) {
	var result options
	if len(args) == 0 {
		return result, errors.New("usage: go run ./cmd/project <quality|build|test|package|bootstrap|install-hooks|commit-msg|version> [options]")
	}
	result.command = args[0]
	allowed := map[string]string{
		"quality": "stage tag message-file commit-range staged github-event verbose",
		"build":   "verbose", "test": "verbose", "package": "verbose",
		"bootstrap": "verbose", "install-hooks": "verbose",
		"commit-msg": "message-file commit-range github-event verbose", "version": "tag verbose",
	}
	permitted, ok := allowed[result.command]
	if !ok {
		return result, fmt.Errorf("unknown command %q", result.command)
	}
	flags := flag.NewFlagSet(result.command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Var(&result.stages, "stage", "quality stage (repeatable)")
	flags.StringVar(&result.tag, "tag", "", "release tag matching VERSION")
	flags.StringVar(&result.messageFile, "message-file", "", "commit message file")
	flags.StringVar(&result.commitRange, "commit-range", "", "Git revision range")
	flags.BoolVar(&result.staged, "staged", false, "check exact Git index contents")
	flags.BoolVar(&result.githubEvent, "github-event", false, "validate GitHub event commits")
	flags.BoolVar(&result.verbose, "verbose", false, "show successful tool output")
	if err := flags.Parse(args[1:]); err != nil {
		return result, err
	}
	if flags.NArg() != 0 {
		return result, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	var invalid error
	flags.Visit(func(item *flag.Flag) {
		if !strings.Contains(" "+permitted+" ", " "+item.Name+" ") {
			invalid = errors.Join(invalid, fmt.Errorf("--%s is not supported by %s", item.Name, result.command))
		}
	})
	if invalid != nil {
		return result, invalid
	}
	for _, selected := range result.stages {
		if !slices.Contains(stages, selected) {
			return result, fmt.Errorf("unknown quality stage %q", selected)
		}
	}
	if result.staged && (len(result.stages) != 0 || result.tag != "" || result.messageFile != "" || result.commitRange != "" || result.githubEvent) {
		return result, errors.New("--staged requires the full source gate without extra event inputs")
	}
	if result.command == "commit-msg" && result.messageFile == "" && result.commitRange == "" && !result.githubEvent {
		return result, errors.New("commit-msg requires --message-file, --commit-range or --github-event")
	}
	return result, nil
}

func dispatch(args []string) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		printHelp()
		return nil
	}
	settings, err := parseOptions(args)
	if errors.Is(err, flag.ErrHelp) {
		printHelp()
		return nil
	}
	if err != nil {
		return err
	}
	root, err := repositoryRoot(settings.command)
	if err != nil {
		return err
	}
	c, err := newController(root, settings.verbose)
	if err != nil {
		return err
	}
	switch settings.command {
	case "quality":
		if settings.staged {
			err = c.stagedQuality()
		} else {
			err = c.quality(settings.stages, settings.tag)
			if err == nil {
				err = c.commitMessages(settings.messageFile, settings.commitRange)
			}
			if err == nil && settings.githubEvent {
				err = c.githubEventMessages()
			}
		}
		if err == nil {
			fmt.Println("quality passed")
		}
	case "version":
		_, err = c.version(settings.tag)
	case "build":
		err = c.build()
	case "test":
		err = c.test(false)
	case "package":
		err = c.bundle()
	case "bootstrap":
		err = c.bootstrap()
	case "install-hooks":
		err = c.installHooks()
	case "commit-msg":
		err = c.commitMessages(settings.messageFile, settings.commitRange)
		if err == nil && settings.githubEvent {
			err = c.githubEventMessages()
		}
	}
	return err
}

func printHelp() {
	fmt.Println("Usage: go run ./cmd/project <command> [options]")
	fmt.Println("Commands: quality, build, test, package, bootstrap, install-hooks, commit-msg, version")
	fmt.Println("Quality stages:", strings.Join(stages, ", "))
	fmt.Println("Options: --stage NAME (repeatable), --tag TAG, --message-file PATH, --commit-range RANGE, --staged, --github-event, --verbose")
}

func repositoryRoot(command string) (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for candidate := current; ; candidate = filepath.Dir(candidate) {
		if regularFile(filepath.Join(candidate, "go.mod")) && (regularFile(filepath.Join(candidate, "VERSION")) || command == "commit-msg" || command == "install-hooks") {
			return candidate, nil
		}
		if filepath.Dir(candidate) == candidate {
			break
		}
	}
	// Message hooks also work in an isolated Git fixture without product files.
	if command == "commit-msg" || command == "install-hooks" {
		probe := exec.Command("git", "rev-parse", "--show-toplevel")
		probe.Dir = current
		output, probeErr := probe.Output()
		if probeErr == nil {
			candidate := strings.TrimSpace(string(output))
			if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
				return filepath.Abs(candidate)
			}
		}
	}
	return "", errors.New("cannot locate repository root from current directory (go.mod and VERSION required)")
}

func regularFile(name string) bool {
	info, err := os.Lstat(name)
	return err == nil && info.Mode().IsRegular()
}

func newController(root string, verbose bool) (*controller, error) {
	out := filepath.Join(root, "out")
	tools := os.Getenv("WORKBUDDY_TOOLS_ROOT")
	if tools == "" {
		tools = filepath.Join(out, "tools")
	}
	tools, err := filepath.Abs(tools)
	if err != nil {
		return nil, err
	}
	c := &controller{root: root, out: out, tools: tools, verbose: verbose}
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, found := strings.Cut(entry, "=")
		if found {
			env[key] = value
		}
	}
	locations := map[string]string{
		"GOCACHE": "cache/go-build", "GOMODCACHE": "cache/go-mod", "GOPATH": "cache/gopath",
		"GOBIN": "tools/bin", "GOTMPDIR": "tmp/go", "GOLANGCI_LINT_CACHE": "cache/golangci-lint",
		"GOPLSCACHE": "cache", "XDG_CACHE_HOME": "cache/xdg", "XDG_CONFIG_HOME": "config",
		"XDG_DATA_HOME": "data", "TMPDIR": "tmp", "TMP": "tmp", "TEMP": "tmp",
		"GOENV": "config/go.env", "GOTELEMETRYDIR": "telemetry",
	}
	for key, relative := range locations {
		location := filepath.Join(out, filepath.FromSlash(relative))
		directory := location
		if key == "GOENV" {
			directory = filepath.Dir(location)
		}
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return nil, err
		}
		env[key] = location
	}
	if err := os.MkdirAll(filepath.Join(out, "logs"), 0o755); err != nil {
		return nil, err
	}
	// A module boundary keeps generated Go files and dependency caches out of
	// root ./... scans, including cached dependencies without their own go.mod.
	artifactModule := filepath.Join(out, "go.mod")
	if _, err := os.Stat(artifactModule); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(artifactModule, []byte("module cpa-workbuddy/artifacts\n"), 0o644); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	maps.Copy(env, map[string]string{
		"CGO_ENABLED": "1", "GOFLAGS": "-mod=readonly", "GOTOOLCHAIN": "local",
		"GOWORK": "off", "GOTELEMETRY": "off", "WORKBUDDY_TOOLS_ROOT": tools,
	})
	for _, key := range []string{"GOOS", "GOARCH", "GODEBUG", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS"} {
		delete(env, key)
	}
	for key, value := range env {
		c.env = append(c.env, key+"="+value)
	}
	sort.Strings(c.env)
	return c, nil
}

func (c *controller) execute(args []string, policy runPolicy) ([]byte, error) {
	stdout, stderr, err := c.capture(args)
	stdoutNonempty := len(bytes.TrimSpace(stdout)) != 0
	stderrNonempty := len(bytes.TrimSpace(stderr)) != 0
	failed := err != nil || (policy.nonemptyFails && (stdoutNonempty || stderrNonempty)) || (policy.stderrFails && stderrNonempty)
	if failed || c.verbose {
		showCommand(args, stdout, stderr)
	}
	if failed {
		if err != nil {
			return nil, fmt.Errorf("command failed: %s: %w", args[0], err)
		}
		return nil, fmt.Errorf("command emitted blocking diagnostics: %s", args[0])
	}
	return stdout, nil
}

func (c *controller) capture(args []string) ([]byte, []byte, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = c.root
	cmd.Env = c.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func showCommand(args []string, stdout, stderr []byte) {
	quoted := make([]string, len(args))
	for index, arg := range args {
		quoted[index] = fmt.Sprintf("%q", arg)
	}
	fmt.Fprintln(os.Stderr, "$", strings.Join(quoted, " "))
	for _, output := range [][]byte{stdout, stderr} {
		if len(output) != 0 {
			fmt.Fprint(os.Stderr, string(output))
			if output[len(output)-1] != '\n' {
				fmt.Fprintln(os.Stderr)
			}
		}
	}
}

func (c *controller) executable(name string) (string, error) {
	local := filepath.Join(c.tools, "bin", name+executableSuffix())
	if regularFile(local) {
		return local, nil
	}
	found, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("missing %s; run go run ./cmd/project bootstrap: %w", name, err)
	}
	return filepath.Abs(found)
}

func executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func (c *controller) tool(name string, policy runPolicy, args ...string) ([]byte, error) {
	binary, err := c.executable(name)
	if err != nil {
		return nil, err
	}
	return c.execute(append([]string{binary}, args...), policy)
}

func (c *controller) ownedFiles(suffix string) ([]string, error) {
	excluded := []string{"out", ".git", "_ref", "vendor", "node_modules"}
	var files []string
	err := filepath.WalkDir(c.root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name != c.root && slices.Contains(excluded, entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("owned source cannot be a symlink: %s", name)
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), suffix) {
			files = append(files, name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no owned %s files discovered", suffix)
	}
	return files, nil
}

func (c *controller) checkTools() error {
	banner, err := c.tool("go", runPolicy{}, "version")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(banner), "go version "+goVersion+" ") {
		return fmt.Errorf("go toolchain %s required; found %s", goVersion, strings.TrimSpace(string(banner)))
	}
	banner, err = c.tool("gopls", runPolicy{}, "version")
	if err != nil {
		return err
	}
	if !slices.Contains(strings.Fields(string(banner)), goplsVersion) {
		return fmt.Errorf("gopls %s required; found %s", goplsVersion, strings.TrimSpace(string(banner)))
	}
	banner, err = c.tool("golangci-lint", runPolicy{}, "version")
	if err != nil {
		return err
	}
	if !strings.Contains(string(banner), "version "+golangciVersion+" ") || !slices.Contains(strings.Fields(string(banner)), goVersion) {
		return fmt.Errorf("golangci-lint %s, built with %s, required; found %s", golangciVersion, goVersion, strings.TrimSpace(string(banner)))
	}
	binary, err := c.executable("gopls")
	if err != nil {
		return err
	}
	args := []string{binary, "help", "check"}
	stdout, stderr, err := c.capture(args)
	if err != nil || c.verbose {
		showCommand(args, stdout, stderr)
	}
	if err != nil {
		return fmt.Errorf("gopls help check failed: %w", err)
	}
	if !bytes.Contains(stdout, []byte("-severity=string")) && !bytes.Contains(stderr, []byte("-severity=string")) {
		return errors.New("gopls check severity option is unavailable")
	}
	return nil
}

type upstreamRecord struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	SHA1   string `json:"git_blob_sha1"`
}

func safeRelative(relative string) bool {
	if relative == "" || strings.ContainsAny(relative, "\\:\x00") || path.IsAbs(relative) || path.Clean(relative) != relative || !filepath.IsLocal(filepath.FromSlash(relative)) {
		return false
	}
	for component := range strings.SplitSeq(relative, "/") {
		if runtime.GOOS == "windows" && strings.TrimRight(component, ". ") != component {
			return false
		}
		if component == "." || component == ".." || strings.EqualFold(component, ".git") {
			return false
		}
	}
	return true
}

func (c *controller) upstream() error {
	base := filepath.Join(c.root, "_ref")
	if err := rejectSymlinkPath(base, "UPSTREAM.json"); err != nil {
		return fmt.Errorf("unsafe upstream manifest: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(base, "UPSTREAM.json"))
	if err != nil {
		return err
	}
	var manifest struct {
		Files []upstreamRecord `json:"files"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if len(manifest.Files) != 45 {
		return errors.New("upstream manifest must declare exactly 45 files")
	}
	expected := map[string]bool{"UPSTREAM.json": true}
	for _, record := range manifest.Files {
		if !safeRelative(record.Path) {
			return fmt.Errorf("unsafe upstream path: %s", record.Path)
		}
		if expected[record.Path] {
			return fmt.Errorf("duplicate upstream path: %s", record.Path)
		}
		expected[record.Path] = true
		name := filepath.Join(base, filepath.FromSlash(record.Path))
		if err := rejectSymlinkPath(base, record.Path); err != nil {
			return fmt.Errorf("unsafe upstream path %s: %w", record.Path, err)
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		sha := sha256.Sum256(content)
		blob := sha1.New()
		if _, err := fmt.Fprintf(blob, "blob %d\x00", len(content)); err != nil {
			return err
		}
		if _, err := blob.Write(content); err != nil {
			return err
		}
		if int64(len(content)) != record.Size || hex.EncodeToString(sha[:]) != record.SHA256 || hex.EncodeToString(blob.Sum(nil)) != record.SHA1 {
			return fmt.Errorf("upstream bytes differ: _ref/%s", record.Path)
		}
	}
	actual := make(map[string]bool)
	err = filepath.WalkDir(base, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe upstream symlink: %s", name)
		}
		if !entry.IsDir() {
			relative, err := filepath.Rel(base, name)
			if err != nil {
				return err
			}
			actual[filepath.ToSlash(relative)] = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	var difference []string
	for name := range actual {
		if !expected[name] {
			difference = append(difference, name)
		}
	}
	for name := range expected {
		if !actual[name] {
			difference = append(difference, name)
		}
	}
	if len(difference) != 0 {
		sort.Strings(difference)
		return fmt.Errorf("upstream inventory differs: %v", difference)
	}
	return nil
}

func rejectSymlinkPath(base, relative string) error {
	current := base
	components := append([]string{""}, strings.Split(relative, "/")...)
	for _, component := range components {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink: %s", current)
		}
	}
	return nil
}

func (c *controller) version(tag string) (string, error) {
	data, err := os.ReadFile(filepath.Join(c.root, "VERSION"))
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if !semverPattern.MatchString(value) {
		return "", errors.New("VERSION must contain a SemVer product version")
	}
	source, err := os.ReadFile(filepath.Join(c.root, "version.go"))
	if err != nil {
		return "", err
	}
	if !bytes.Contains(source, []byte("//go:embed VERSION")) {
		return "", errors.New("product version must embed root VERSION")
	}
	tagSupplied := tag != ""
	if !tagSupplied {
		tag, tagSupplied = os.LookupEnv("RELEASE_TAG")
	}
	if tagSupplied && tag != "v"+value {
		return "", fmt.Errorf("release tag %q must equal v%s", tag, value)
	}
	return value, nil
}

func (c *controller) formatCheck() error {
	files, err := c.ownedFiles(".go")
	if err != nil {
		return err
	}
	_, err = c.tool("gofmt", runPolicy{nonemptyFails: true}, append([]string{"-l"}, files...)...)
	return err
}

func (c *controller) modules() error {
	files, err := c.ownedFiles("go.mod")
	if err != nil {
		return err
	}
	for _, name := range files {
		if name != filepath.Join(c.root, "go.mod") {
			return fmt.Errorf("undeclared nested Go module: %s", name)
		}
	}
	if _, err := c.tool("go", runPolicy{nonemptyFails: true}, "mod", "tidy", "-diff"); err != nil {
		return err
	}
	_, err = c.tool("go", runPolicy{}, "mod", "verify")
	return err
}

func (c *controller) vet() error {
	_, err := c.tool("go", runPolicy{nonemptyFails: true}, "vet", "./...")
	return err
}

func (c *controller) lsp() error {
	files, err := c.ownedFiles(".go")
	if err != nil {
		return err
	}
	args := []string{"-logfile=" + filepath.Join(c.out, "logs", "gopls.log"), "check", "-severity=hint"}
	_, err = c.tool("gopls", runPolicy{nonemptyFails: true}, append(args, files...)...)
	return err
}

func (c *controller) lint() error {
	if _, err := c.tool("golangci-lint", runPolicy{}, "config", "verify"); err != nil {
		return err
	}
	_, err := c.tool("golangci-lint", runPolicy{nonemptyFails: true}, "run", "--show-stats=false", "--max-issues-per-linter=0", "--max-same-issues=0", "--uniq-by-line=false", "./...")
	return err
}

func (c *controller) libraryPath() (string, error) {
	var extension string
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		extension = ".dll"
	case "linux/amd64", "linux/arm64":
		extension = ".so"
	case "darwin/amd64", "darwin/arm64":
		extension = ".dylib"
	default:
		return "", fmt.Errorf("unsupported native target: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return filepath.Join(c.out, "bin", "cpa-workbuddy"+extension), nil
}

func headerPath(artifact string) string {
	return strings.TrimSuffix(artifact, filepath.Ext(artifact)) + ".h"
}

func (c *controller) build() error {
	if _, err := c.version(""); err != nil {
		return err
	}
	artifact, err := c.libraryPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		return err
	}
	for _, name := range []string{artifact, headerPath(artifact)} {
		if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if _, err := c.tool("go", runPolicy{nonemptyFails: true}, "build", "-trimpath", "-buildmode=c-shared", "-o", artifact, "./cmd/cpa-workbuddy"); err != nil {
		return err
	}
	info, err := os.Stat(artifact)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("missing c-shared artifact: %s", artifact)
	}
	header, err := os.ReadFile(headerPath(artifact))
	if err != nil {
		return err
	}
	if !bytes.Contains(header, []byte("cliproxy_plugin_init")) {
		return errors.New("generated C header must declare cliproxy_plugin_init")
	}
	return nil
}

func (c *controller) test(prebuilt bool) error {
	if !prebuilt {
		if err := c.build(); err != nil {
			return err
		}
	}
	artifact, err := c.libraryPath()
	if err != nil {
		return err
	}
	if !regularFile(artifact) {
		return fmt.Errorf("native smoke requires prebuilt library: %s", artifact)
	}
	child := *c
	child.env = make([]string, 0, len(c.env)+1)
	for _, entry := range c.env {
		if !strings.HasPrefix(entry, "WORKBUDDY_TEST_LIBRARY=") {
			child.env = append(child.env, entry)
		}
	}
	child.env = append(child.env, "WORKBUDDY_TEST_LIBRARY="+artifact)
	output, err := child.tool("go", runPolicy{stderrFails: true}, "test", "-json", "-count=1", "./...")
	if err != nil {
		return err
	}
	var passed, native bool
	for line := range bytes.SplitSeq(output, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event struct {
			Action  string
			Package string
			Test    string
		}
		if err := json.Unmarshal(line, &event); err != nil {
			fmt.Fprint(os.Stderr, string(output))
			return fmt.Errorf("invalid Go test JSON: %w", err)
		}
		if event.Action == "pass" && event.Test != "" {
			passed = true
			if event.Package == "cpa-workbuddy" && event.Test == "TestNativeABI" {
				native = true
			}
		}
	}
	if !passed {
		fmt.Fprint(os.Stderr, string(output))
		return errors.New("go tests discovered no passing tests")
	}
	if !native {
		fmt.Fprint(os.Stderr, string(output))
		return errors.New("native smoke test did not execute")
	}
	return nil
}

func (c *controller) bundle() (resultErr error) {
	if err := c.build(); err != nil {
		return err
	}
	value, err := c.version("")
	if err != nil {
		return err
	}
	artifact, err := c.libraryPath()
	if err != nil {
		return err
	}
	info, err := buildinfo.ReadFile(artifact)
	if err != nil {
		return err
	}
	buildInfo := fmt.Appendf(nil, "product\tcpa-workbuddy\nversion\t%s\n%s", value, info.String())
	goRoot, err := c.tool("go", runPolicy{stderrFails: true}, "env", "GOROOT")
	if err != nil {
		return err
	}
	toolchainRoot := strings.TrimSpace(string(goRoot))
	if !filepath.IsAbs(toolchainRoot) {
		return errors.New("toolchain must provide an absolute GOROOT for license notices")
	}
	directory := filepath.Join(c.out, "dist")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	archive := filepath.Join(directory, fmt.Sprintf("cpa-workbuddy-v%s-%s-%s.zip", value, runtime.GOOS, runtime.GOARCH))
	file, err := os.Create(archive)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	archiveHash := sha256.New()
	writer := zip.NewWriter(io.MultiWriter(file, archiveHash))
	files := [][2]string{
		{artifact, filepath.Base(artifact)},
		{headerPath(artifact), filepath.Base(headerPath(artifact))},
		{filepath.Join(c.root, "cmd", "cpa-workbuddy", "native.h"), "native.h"},
		{filepath.Join(toolchainRoot, "LICENSE"), "licenses/Go-LICENSE"},
		{filepath.Join(toolchainRoot, "PATENTS"), "licenses/Go-PATENTS"},
	}
	for _, relative := range []string{
		"VERSION", "LICENSE", "README.md",
		"docs/requirements.md", "docs/architecture.md", "docs/compatibility.md",
		"docs/quality.md", "docs/deployment.md", "docs/third-party-notices.md",
		"docs/tasks/workbuddy-plugin.md", "docs/licenses/CLIProxyAPI-MIT.txt",
		"docs/patches/cpa-v7.3.15.patch",
	} {
		files = append(files, [2]string{filepath.Join(c.root, filepath.FromSlash(relative)), relative})
	}
	var checksums []byte
	for _, member := range files {
		digest, err := addZipFile(writer, member[0], member[1])
		if err != nil {
			return errors.Join(err, writer.Close())
		}
		checksums = fmt.Appendf(checksums, "%x  %s\n", digest, member[1])
	}
	metadata, err := writer.Create("build-info.txt")
	if err != nil {
		return errors.Join(err, writer.Close())
	}
	if _, err := metadata.Write(buildInfo); err != nil {
		return errors.Join(err, writer.Close())
	}
	checksums = fmt.Appendf(checksums, "%x  build-info.txt\n", sha256.Sum256(buildInfo))
	manifest, err := writer.Create("checksums.sha256")
	if err != nil {
		return errors.Join(err, writer.Close())
	}
	if _, err := manifest.Write(checksums); err != nil {
		return errors.Join(err, writer.Close())
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return os.WriteFile(archive+".sha256", fmt.Appendf(nil, "%x  %s\n", archiveHash.Sum(nil), filepath.Base(archive)), 0o644)
}

func addZipFile(writer *zip.Writer, name, destination string) (digest [sha256.Size]byte, resultErr error) {
	if !regularFile(name) {
		return digest, fmt.Errorf("missing or unsafe package member: %s", name)
	}
	file, err := os.Open(name)
	if err != nil {
		return digest, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return digest, err
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return digest, err
	}
	header.Name = destination
	header.Method = zip.Deflate
	member, err := writer.CreateHeader(header)
	if err != nil {
		return digest, err
	}
	memberHash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(member, memberHash), file); err != nil {
		return digest, err
	}
	memberHash.Sum(digest[:0])
	return digest, nil
}

func validateMessage(message []byte) error {
	if !utf8.Valid(message) {
		return errors.New("commit message must be valid UTF-8")
	}
	text := strings.TrimPrefix(string(message), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if text == "" || lines[0] == "" {
		return errors.New("commit message is empty")
	}
	subject := lines[0]
	if !commitPattern.MatchString(subject) && !mergePattern.MatchString(subject) && !revertPattern.MatchString(subject) {
		return fmt.Errorf("invalid Conventional Commit subject: %q", subject)
	}
	if utf8.RuneCountInString(subject) > 100 {
		return errors.New("commit subject exceeds 100 characters")
	}
	if len(lines) > 1 && strings.TrimSpace(lines[1]) != "" {
		return errors.New("commit subject and body must be separated by a blank line")
	}
	return nil
}

func (c *controller) commitMessages(messageFile, commitRange string) error {
	if messageFile != "" {
		if !filepath.IsAbs(messageFile) {
			messageFile = filepath.Join(c.root, messageFile)
		}
		message, err := os.ReadFile(messageFile)
		if err != nil {
			return err
		}
		if err := validateMessage(message); err != nil {
			return err
		}
	}
	if commitRange == "" {
		return nil
	}
	if strings.HasPrefix(commitRange, "-") {
		return errors.New("commit range cannot begin with '-'")
	}
	output, err := c.execute([]string{"git", "rev-list", "--reverse", commitRange, "--"}, runPolicy{})
	if err != nil {
		return err
	}
	commits := strings.Fields(string(output))
	if len(commits) == 0 {
		return errors.New("commit range selected no commits")
	}
	for _, commit := range commits {
		message, err := c.execute([]string{"git", "show", "-s", "--format=%B", commit}, runPolicy{})
		if err != nil {
			return err
		}
		if err := validateMessage(message); err != nil {
			return fmt.Errorf("commit %s: %w", commit, err)
		}
	}
	return nil
}

func (c *controller) githubEventMessages() error {
	eventPath := os.Getenv("GITHUB_EVENT_PATH")
	if eventPath == "" {
		return errors.New("--github-event requires GITHUB_EVENT_PATH")
	}
	data, err := os.ReadFile(eventPath)
	if err != nil {
		return err
	}
	var event struct {
		Before      string `json:"before"`
		After       string `json:"after"`
		PullRequest struct {
			Base struct {
				SHA string `json:"sha"`
			} `json:"base"`
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}
	var commitRange string
	switch os.Getenv("GITHUB_EVENT_NAME") {
	case "pull_request":
		if !validObjectID(event.PullRequest.Base.SHA) || !validObjectID(event.PullRequest.Head.SHA) {
			return errors.New("pull_request event requires valid base and head commit IDs")
		}
		commitRange = event.PullRequest.Base.SHA + ".." + event.PullRequest.Head.SHA
	case "push":
		if !validObjectID(event.Before) || !validObjectID(event.After) {
			return errors.New("push event requires valid before and after commit IDs")
		}
		commitRange = event.After
		if strings.Trim(event.Before, "0") != "" {
			commitRange = event.Before + ".." + event.After
		}
	default:
		return errors.New("commit message CI accepts pull_request and push events")
	}
	return c.commitMessages("", commitRange)
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (c *controller) bootstrap() error {
	banner, err := c.tool("go", runPolicy{}, "version")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(banner), "go version "+goVersion+" ") {
		return fmt.Errorf("go toolchain %s required; found %s", goVersion, strings.TrimSpace(string(banner)))
	}
	for _, tool := range []struct{ name, version, module string }{
		{"gopls", goplsVersion, "golang.org/x/tools/gopls@" + goplsVersion},
		{"golangci-lint", golangciVersion, "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v" + golangciVersion},
	} {
		binary, lookupErr := c.executable(tool.name)
		if lookupErr == nil {
			args := []string{binary, "version"}
			stdout, stderr, runErr := c.capture(args)
			if runErr != nil || c.verbose {
				showCommand(args, stdout, stderr)
			}
			text := string(stdout)
			matched := slices.Contains(strings.Fields(text), tool.version)
			if tool.name == "golangci-lint" {
				matched = strings.Contains(text, "version "+tool.version+" ") && slices.Contains(strings.Fields(text), goVersion)
			}
			if runErr == nil && matched {
				continue
			}
		}
		if _, err := c.tool("go", runPolicy{}, "install", tool.module); err != nil {
			return err
		}
	}
	return c.checkTools()
}

func (c *controller) gitConfig(key string) (string, bool, error) {
	args := []string{"git", "config", "--get", key}
	stdout, stderr, err := c.capture(args)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", false, nil
		}
		showCommand(args, stdout, stderr)
		return "", false, fmt.Errorf("reading Git configuration %s: %w", key, err)
	}
	if c.verbose {
		showCommand(args, stdout, stderr)
	}
	return strings.TrimSpace(string(stdout)), true, nil
}

func (c *controller) installHooks() error {
	current, _, err := c.gitConfig("core.hooksPath")
	if err != nil {
		return err
	}
	hooks := filepath.Join(c.root, "scripts", "git-hooks")
	if current != "" {
		existing := current
		if !filepath.IsAbs(existing) {
			existing = filepath.Join(c.root, existing)
		}
		if filepath.Clean(existing) != filepath.Clean(hooks) {
			return fmt.Errorf("existing hooksPath %q; preserve and integrate it explicitly", current)
		}
	} else {
		output, err := c.execute([]string{"git", "rev-parse", "--git-path", "hooks"}, runPolicy{})
		if err != nil {
			return err
		}
		defaultHooks := strings.TrimSpace(string(output))
		if !filepath.IsAbs(defaultHooks) {
			defaultHooks = filepath.Join(c.root, defaultHooks)
		}
		entries, err := os.ReadDir(defaultHooks)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		var active []string
		for _, entry := range entries {
			if !entry.IsDir() && !strings.HasSuffix(entry.Name(), ".sample") {
				active = append(active, entry.Name())
			}
		}
		if len(active) != 0 {
			return fmt.Errorf("existing native hooks must be integrated first: %v", active)
		}
	}
	goBinary, err := c.executable("go")
	if err != nil {
		return err
	}
	goBinary, err = filepath.EvalSymlinks(goBinary)
	if err != nil {
		return err
	}
	goBinary, err = filepath.Abs(goBinary)
	if err != nil {
		return err
	}
	for _, name := range []string{"pre-commit", "commit-msg"} {
		filename := filepath.Join(hooks, name)
		info, err := os.Lstat(filename)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsafe hook file: %s", filename)
		}
		if err := os.Chmod(filename, info.Mode()|0o111); err != nil {
			return err
		}
	}
	if _, err := c.execute([]string{"git", "config", "--local", "workbuddy.go", filepath.ToSlash(goBinary)}, runPolicy{}); err != nil {
		return err
	}
	_, err = c.execute([]string{"git", "config", "--local", "core.hooksPath", filepath.ToSlash(hooks)}, runPolicy{})
	return err
}

func (c *controller) stagedQuality() (resultErr error) {
	directory, err := os.MkdirTemp(filepath.Join(c.out, "tmp"), "index-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(directory)) }()
	listing, err := c.execute([]string{"git", "ls-files", "--stage", "-z"}, runPolicy{})
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	for entry := range bytes.SplitSeq(listing, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		metadata, relative, found := strings.Cut(string(entry), "\t")
		fields := strings.Fields(metadata)
		if !found || len(fields) != 3 {
			return errors.New("malformed Git index entry")
		}
		mode, oid, stage := fields[0], fields[1], fields[2]
		if stage != "0" || (mode != "100644" && mode != "100755") {
			return fmt.Errorf("unsupported/unmerged index entry: %s (%s, %s)", relative, mode, stage)
		}
		identity := relative
		if runtime.GOOS == "windows" {
			identity = strings.ToLower(relative)
		}
		if !safeRelative(relative) || !validObjectID(oid) || seen[identity] {
			return fmt.Errorf("unsafe index path or object: %s", relative)
		}
		seen[identity] = true
		target := filepath.Join(directory, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		content, err := c.execute([]string{"git", "cat-file", "blob", oid}, runPolicy{})
		if err != nil {
			return err
		}
		permissions := fs.FileMode(0o644)
		if mode == "100755" {
			permissions = 0o755
		}
		if err := os.WriteFile(target, content, permissions); err != nil {
			return err
		}
	}
	if !regularFile(filepath.Join(directory, "cmd", "project", "main.go")) {
		return errors.New("stage cmd/project/main.go before running the index gate")
	}
	snapshot, err := newController(directory, c.verbose)
	if err != nil {
		return err
	}
	// Tools are shared, while all caches, temporary files and artifacts stay in the snapshot's out directory.
	snapshot.tools = c.tools
	for index, entry := range snapshot.env {
		if strings.HasPrefix(entry, "WORKBUDDY_TOOLS_ROOT=") {
			snapshot.env[index] = "WORKBUDDY_TOOLS_ROOT=" + c.tools
		}
	}
	binary := filepath.Join(snapshot.out, "bin", "project"+executableSuffix())
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		return err
	}
	if _, err := snapshot.tool("go", runPolicy{nonemptyFails: true}, "build", "-trimpath", "-o", binary, "./cmd/project"); err != nil {
		return err
	}
	args := []string{binary, "quality"}
	if c.verbose {
		args = append(args, "--verbose")
	}
	_, err = snapshot.execute(args, runPolicy{})
	return err
}

func (c *controller) quality(selected []string, tag string) error {
	actions := map[string]func() error{
		"tools": c.checkTools, "upstream": c.upstream,
		"version": func() error { _, err := c.version(tag); return err },
		"format":  c.formatCheck, "modules": c.modules, "vet": c.vet,
		"lsp": c.lsp, "lint": c.lint, "build": c.build,
		"test": func() error { return c.test(len(selected) == 0 || slices.Contains(selected, "build")) },
	}
	for _, stage := range stages {
		if len(selected) != 0 && !slices.Contains(selected, stage) {
			continue
		}
		fmt.Printf("[%s]\n", stage)
		if err := actions[stage](); err != nil {
			return fmt.Errorf("%s: %w", stage, err)
		}
	}
	return nil
}

package repository

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RuntimeEnvironment is the resolved, validated toolchain environment for one
// immutable candidate checkout. Its implementation deliberately stays opaque:
// callers can report the versions and hand the value back to the repository
// adapter, but do not need to know how a local manager exposed each executable.
type RuntimeEnvironment struct {
	versions    map[string]string
	environment []string
	shimPath    string
}

// Versions returns the actual runtime versions that were validated. A copy is
// returned so Evidence cannot be changed by later mutation of the resolver.
func (runtime RuntimeEnvironment) Versions() map[string]string {
	versions := make(map[string]string, len(runtime.versions))
	for name, version := range runtime.versions {
		versions[name] = version
	}
	return versions
}

// Summary is the stable, compact representation printed before verification.
func (runtime RuntimeEnvironment) Summary() string {
	if len(runtime.versions) == 0 {
		return ""
	}
	names := make([]string, 0, len(runtime.versions))
	for name := range runtime.versions {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+" "+runtime.versions[name])
	}
	return strings.Join(parts, ", ")
}

// Close removes the private command directory created for this environment.
// It is safe to call for the zero environment used by repositories without a
// runtime declaration.
func (runtime RuntimeEnvironment) Close() error {
	if runtime.shimPath == "" {
		return nil
	}
	return os.RemoveAll(runtime.shimPath)
}

type runtimeMatch int

const (
	matchPrefix runtimeMatch = iota
	matchMinimum
	matchRange
)

type runtimeDeclaration struct {
	name        string
	requirement string
	source      string
	precedence  int
	match       runtimeMatch
}

type runtimeRequirement struct {
	name, requirement, source string
	declarations              []runtimeDeclaration
}

// ResolveRuntime discovers the runtime contract inside the candidate checkout,
// resolves only already-installed executables, and validates their actual
// versions before returning an environment suitable for the canonical check.
// Repositories with no supported declaration preserve the caller's environment.
func ResolveRuntime(checkout string) (RuntimeEnvironment, error) {
	requirements, err := discoverRuntime(checkout)
	if err != nil {
		return RuntimeEnvironment{}, err
	}
	if len(requirements) == 0 {
		return RuntimeEnvironment{}, nil
	}
	versions := make(map[string]string, len(requirements))
	executables := make(map[string]string, len(requirements))
	var directories []string
	for _, requirement := range requirements {
		executable, actual, err := resolveExecutable(checkout, requirement)
		if err != nil {
			return RuntimeEnvironment{}, err
		}
		versions[requirement.name] = actual
		executables[requirement.name] = executable
		directories = appendUnique(directories, filepath.Dir(executable))
	}
	shimPath, err := buildRuntimeCommandDirectory(executables)
	if err != nil {
		return RuntimeEnvironment{}, err
	}
	pathDirectories := append([]string{shimPath}, directories...)
	path := strings.Join(append(pathDirectories, filepath.SplitList(os.Getenv("PATH"))...), string(os.PathListSeparator))
	return RuntimeEnvironment{versions: versions, environment: []string{"PATH=" + path, "MISE_AUTO_INSTALL=0"}, shimPath: shimPath}, nil
}

func buildRuntimeCommandDirectory(executables map[string]string) (string, error) {
	directory, err := os.MkdirTemp("", "forgepilot-runtime-")
	if err != nil {
		return "", fmt.Errorf("create runtime environment: %w", err)
	}
	fail := func(err error) (string, error) {
		_ = os.RemoveAll(directory)
		return "", err
	}
	for name, executable := range executables {
		absolute, err := filepath.Abs(executable)
		if err != nil {
			return fail(fmt.Errorf("resolve %s executable: %w", name, err))
		}
		for _, alias := range runtimeExecutableNames(name) {
			if err := os.Symlink(absolute, filepath.Join(directory, alias)); err != nil {
				return fail(fmt.Errorf("create runtime environment for %s: %w", name, err))
			}
		}
	}
	return directory, nil
}

func discoverRuntime(checkout string) ([]runtimeRequirement, error) {
	var declarations []runtimeDeclaration
	add := func(name, requirement, source string, precedence int, match runtimeMatch) error {
		name = runtimeName(name)
		if name == "" {
			return nil
		}
		requirement = normalizeRequirement(name, requirement)
		if requirement == "" {
			return fmt.Errorf("%s does not declare a %s version", source, runtimeDisplayName(name))
		}
		declarations = append(declarations, runtimeDeclaration{name: name, requirement: requirement, source: source, precedence: precedence, match: match})
		return nil
	}

	if contents, ok, err := optionalRuntimeFile(checkout, "mise.toml"); err != nil {
		return nil, err
	} else if ok {
		values, err := parseMiseTools(contents)
		if err != nil {
			return nil, fmt.Errorf("read mise.toml: %w", err)
		}
		for name, requirement := range values {
			if err := add(name, requirement, "mise.toml", 10, matchPrefix); err != nil {
				return nil, err
			}
		}
	}
	if contents, ok, err := optionalRuntimeFile(checkout, ".tool-versions"); err != nil {
		return nil, err
	} else if ok {
		for _, fields := range runtimeLines(contents) {
			if runtimeName(fields[0]) == "" {
				continue
			}
			if len(fields) < 2 {
				return nil, fmt.Errorf(".tool-versions does not declare a %s version", runtimeDisplayName(runtimeName(fields[0])))
			}
			if err := add(fields[0], fields[1], ".tool-versions", 20, matchPrefix); err != nil {
				return nil, err
			}
		}
	}
	for index, file := range []string{".node-version", ".nvmrc"} {
		if contents, ok, err := optionalRuntimeFile(checkout, file); err != nil {
			return nil, err
		} else if ok {
			if err := add("node", firstRuntimeToken(contents), file, 30+index, matchPrefix); err != nil {
				return nil, err
			}
		}
	}
	if contents, ok, err := optionalRuntimeFile(checkout, "package.json"); err != nil {
		return nil, err
	} else if ok {
		var manifest struct {
			Engines map[string]string `json:"engines"`
		}
		if err := json.Unmarshal([]byte(contents), &manifest); err != nil {
			return nil, fmt.Errorf("read package.json runtime declaration: %w", err)
		}
		if requirement := strings.TrimSpace(manifest.Engines["node"]); requirement != "" {
			if err := add("node", requirement, "package.json engines.node", 40, matchRange); err != nil {
				return nil, err
			}
		}
	}
	if contents, ok, err := optionalRuntimeFile(checkout, "go.mod"); err != nil {
		return nil, err
	} else if ok {
		var languageVersion, toolchain string
		for _, fields := range runtimeLines(contents) {
			if len(fields) >= 2 && fields[0] == "go" {
				languageVersion = fields[1]
			}
			if len(fields) >= 2 && fields[0] == "toolchain" && fields[1] != "default" {
				toolchain = fields[1]
			}
		}
		if toolchain != "" {
			if err := add("go", toolchain, "go.mod toolchain", 30, matchPrefix); err != nil {
				return nil, err
			}
		}
		if languageVersion != "" {
			if err := add("go", languageVersion, "go.mod go", 31, matchMinimum); err != nil {
				return nil, err
			}
		}
	}
	if contents, ok, err := optionalRuntimeFile(checkout, ".python-version"); err != nil {
		return nil, err
	} else if ok {
		if err := add("python", firstRuntimeToken(contents), ".python-version", 30, matchPrefix); err != nil {
			return nil, err
		}
	}
	if contents, ok, err := optionalRuntimeFile(checkout, "rust-toolchain.toml"); err != nil {
		return nil, err
	} else if ok {
		channel, err := parseRustToolchainTOML(contents)
		if err != nil {
			return nil, fmt.Errorf("read rust-toolchain.toml: %w", err)
		}
		if err := add("rust", channel, "rust-toolchain.toml", 30, matchPrefix); err != nil {
			return nil, err
		}
	}
	if contents, ok, err := optionalRuntimeFile(checkout, "rust-toolchain"); err != nil {
		return nil, err
	} else if ok {
		if err := add("rust", firstRuntimeToken(contents), "rust-toolchain", 31, matchPrefix); err != nil {
			return nil, err
		}
	}

	byName := make(map[string][]runtimeDeclaration)
	for _, declaration := range declarations {
		byName[declaration.name] = append(byName[declaration.name], declaration)
	}
	var names []string
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	requirements := make([]runtimeRequirement, 0, len(names))
	for _, name := range names {
		values := byName[name]
		sort.SliceStable(values, func(i, j int) bool { return values[i].precedence < values[j].precedence })
		for i := range values {
			for j := i + 1; j < len(values); j++ {
				if values[i].match == matchPrefix && values[j].match == matchPrefix && !prefixRequirementsCompatible(values[i].requirement, values[j].requirement) {
					return nil, fmt.Errorf("conflicting runtime declarations for %s: %s requires %s, %s requires %s", name, values[i].source, values[i].requirement, values[j].source, values[j].requirement)
				}
			}
		}
		selected := values[0]
		requirements = append(requirements, runtimeRequirement{name: name, requirement: selected.requirement, source: selected.source, declarations: values})
	}
	return requirements, nil
}

func optionalRuntimeFile(root, name string) (string, bool, error) {
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("read %s: runtime declaration must be a regular file", name)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", name, err)
	}
	return string(contents), true, nil
}

func runtimeLines(contents string) [][]string {
	var lines [][]string
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if fields := strings.Fields(line); len(fields) > 0 {
			lines = append(lines, fields)
		}
	}
	return lines
}

func firstRuntimeToken(contents string) string {
	lines := runtimeLines(contents)
	if len(lines) == 0 {
		return ""
	}
	return lines[0][0]
}

var (
	inlineVersion = regexp.MustCompile(`\bversion\s*=\s*["']([^"']+)["']`)
	quotedVersion = regexp.MustCompile(`["']([^"']+)["']`)
)

func parseMiseTools(contents string) (map[string]string, error) {
	tools := make(map[string]string)
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		line := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if section != "tools" || line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || runtimeName(strings.TrimSpace(key)) == "" {
			continue
		}
		value = strings.TrimSpace(value)
		var requirement string
		if strings.HasPrefix(value, "\"") {
			decoded, err := strconv.Unquote(value)
			if err != nil {
				return nil, err
			}
			requirement = decoded
		} else if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
			requirement = value[1 : len(value)-1]
		} else if match := inlineVersion.FindStringSubmatch(value); len(match) == 2 {
			requirement = match[1]
		} else if match := quotedVersion.FindStringSubmatch(value); len(match) == 2 {
			requirement = match[1]
		} else {
			return nil, fmt.Errorf("unsupported %s declaration %q", strings.TrimSpace(key), value)
		}
		name := runtimeName(strings.TrimSpace(key))
		if previous, exists := tools[name]; exists && previous != requirement {
			return nil, fmt.Errorf("conflicting %s entries %q and %q", name, previous, requirement)
		}
		tools[name] = requirement
	}
	return tools, scanner.Err()
}

func parseRustToolchainTOML(contents string) (string, error) {
	section := ""
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if section != "toolchain" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "channel" {
			continue
		}
		decoded, err := strconv.Unquote(strings.TrimSpace(value))
		if err != nil {
			return "", err
		}
		return decoded, nil
	}
	return "", errors.New("toolchain.channel is required")
}

func runtimeName(name string) string {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(name), "\"'")) {
	case "node", "nodejs":
		return "node"
	case "go", "golang":
		return "go"
	case "python", "python3":
		return "python"
	case "rust", "rustc":
		return "rust"
	default:
		return ""
	}
}

func normalizeRequirement(name, requirement string) string {
	requirement = strings.TrimSpace(requirement)
	if name == "node" {
		requirement = strings.TrimPrefix(requirement, "v")
	}
	if name == "go" {
		requirement = strings.TrimPrefix(requirement, "go")
	}
	return requirement
}

func resolveExecutable(checkout string, requirement runtimeRequirement) (string, string, error) {
	candidates := runtimeExecutableCandidates(requirement.name)
	var resolved string
	current := currentRuntimeExecutable(requirement.name)
	for _, candidate := range candidates {
		actual, err := executableVersion(checkout, requirement.name, candidate)
		if err != nil {
			continue
		}
		if resolved == "" || candidate == current {
			resolved = actual
		}
		matches := true
		for _, declaration := range requirement.declarations {
			if !matchesRuntimeDeclaration(declaration, actual, candidate) {
				matches = false
				break
			}
		}
		if matches {
			return candidate, actual, nil
		}
	}
	name := runtimeDisplayName(requirement.name)
	if resolved != "" {
		return "", "", fmt.Errorf("verification environment unavailable: %s %s required by %s, resolved %s", name, requirement.requirement, requirement.source, resolved)
	}
	return "", "", fmt.Errorf("verification environment unavailable: %s %s required by %s, but no installed %s runtime was found", name, requirement.requirement, requirement.source, name)
}

func runtimeExecutableCandidates(name string) []string {
	var candidates []string
	addGlob := func(pattern string) {
		matches, _ := filepath.Glob(pattern)
		sort.Sort(sort.Reverse(sort.StringSlice(matches)))
		candidates = append(candidates, matches...)
	}
	if root := runtimeManagerRoot("MISE_DATA_DIR", filepath.Join(".local", "share", "mise")); root != "" {
		for _, alias := range runtimeManagerAliases(name, "mise") {
			for _, executable := range runtimeExecutableNames(name) {
				addGlob(filepath.Join(root, "installs", alias, "*", "bin", executable))
			}
		}
	}
	if root := runtimeManagerRoot("ASDF_DATA_DIR", ".asdf"); root != "" {
		for _, alias := range runtimeManagerAliases(name, "asdf") {
			for _, executable := range runtimeExecutableNames(name) {
				addGlob(filepath.Join(root, "installs", alias, "*", "bin", executable))
			}
		}
	}
	if name == "node" {
		if root := runtimeManagerRoot("NVM_DIR", ".nvm"); root != "" {
			addGlob(filepath.Join(root, "versions", "node", "*", "bin", "node"))
		}
	}
	if name == "python" {
		if root := runtimeManagerRoot("PYENV_ROOT", ".pyenv"); root != "" {
			for _, executable := range runtimeExecutableNames(name) {
				addGlob(filepath.Join(root, "versions", "*", "bin", executable))
			}
		}
	}
	if name == "rust" {
		if root := runtimeManagerRoot("RUSTUP_HOME", ".rustup"); root != "" {
			addGlob(filepath.Join(root, "toolchains", "*", "bin", "rustc"))
		}
	}
	if current := currentRuntimeExecutable(name); current != "" {
		candidates = append(candidates, current)
		if name == "node" && strings.Contains(filepath.ToSlash(current), "/versions/node/") {
			// NVM-compatible installations put every version beside the current
			// one. This also supports local managers that expose the same layout
			// without sourcing an interactive shell.
			addGlob(filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(current))), "*", "bin", "node"))
		}
	}
	return uniqueStrings(candidates)
}

func runtimeManagerRoot(variable, fallback string) string {
	if root := os.Getenv(variable); root != "" {
		return root
	}
	if variable == "MISE_DATA_DIR" {
		if root := os.Getenv("XDG_DATA_HOME"); root != "" {
			return filepath.Join(root, "mise")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, fallback)
}

func runtimeManagerAliases(name, manager string) []string {
	if manager == "asdf" {
		switch name {
		case "node":
			return []string{"nodejs", "node"}
		case "go":
			return []string{"golang", "go"}
		}
	}
	return []string{name}
}

func runtimeExecutableNames(name string) []string {
	switch name {
	case "python":
		return []string{"python3", "python"}
	case "rust":
		return []string{"rustc"}
	default:
		return []string{name}
	}
}

func currentRuntimeExecutable(name string) string {
	for _, executable := range runtimeExecutableNames(name) {
		if current, err := exec.LookPath(executable); err == nil {
			return current
		}
	}
	return ""
}

func executableVersion(checkout, name, executable string) (string, error) {
	command := exec.Command(executable, "--version")
	command.Dir = checkout
	command.Env = mergedEnvironment([]string{"MISE_AUTO_INSTALL=0"})
	output, err := command.CombinedOutput()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	switch name {
	case "node":
		value = strings.TrimPrefix(value, "v")
	case "go":
		fields := strings.Fields(value)
		if len(fields) >= 3 {
			value = strings.TrimPrefix(fields[2], "go")
		}
	case "python", "rust":
		fields := strings.Fields(value)
		if len(fields) >= 2 {
			value = fields[1]
		}
	}
	if _, ok := numericVersion(value); !ok {
		return "", fmt.Errorf("%s returned an unrecognized version %q", executable, value)
	}
	return value, nil
}

func matchesRuntimeDeclaration(declaration runtimeDeclaration, actual, executable string) bool {
	switch declaration.match {
	case matchPrefix:
		if _, numeric := numericVersion(declaration.requirement); !numeric {
			return declaration.name == "rust" && runtimeChannelMatches(declaration.requirement, executable)
		}
		return versionPrefixMatches(declaration.requirement, actual)
	case matchMinimum:
		required, requiredOK := numericVersion(declaration.requirement)
		resolved, resolvedOK := numericVersion(actual)
		return requiredOK && resolvedOK && compareVersions(resolved, required) >= 0
	case matchRange:
		return matchesVersionRange(declaration.requirement, actual)
	default:
		return false
	}
}

func runtimeChannelMatches(requirement, executable string) bool {
	toolchain := filepath.Base(filepath.Dir(filepath.Dir(executable)))
	return toolchain == requirement || strings.HasPrefix(toolchain, requirement+"-")
}

func versionPrefixMatches(requirement, actual string) bool {
	required, requiredOK := numericVersion(requirement)
	resolved, resolvedOK := numericVersion(actual)
	if !requiredOK || !resolvedOK {
		return false
	}
	for index, part := range required {
		if index >= len(resolved) || resolved[index] != part {
			return false
		}
	}
	return true
}

var numericVersionPattern = regexp.MustCompile(`^(\d+(?:\.\d+){0,3})`)

func numericVersion(value string) ([]int, bool) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(value, "v"), "go"))
	match := numericVersionPattern.FindStringSubmatch(value)
	if len(match) != 2 {
		return nil, false
	}
	var parts []int
	for _, field := range strings.Split(match[1], ".") {
		part, err := strconv.Atoi(field)
		if err != nil {
			return nil, false
		}
		parts = append(parts, part)
	}
	return parts, true
}

func prefixRequirementsCompatible(first, second string) bool {
	a, aOK := numericVersion(first)
	b, bOK := numericVersion(second)
	if !aOK || !bOK {
		return first == second
	}
	length := len(a)
	if len(b) < length {
		length = len(b)
	}
	for index := 0; index < length; index++ {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func matchesVersionRange(requirement, actual string) bool {
	resolved, ok := numericVersion(actual)
	if !ok {
		return false
	}
	for _, alternative := range strings.Split(requirement, "||") {
		if lower, upper, ok := strings.Cut(strings.TrimSpace(alternative), " - "); ok {
			if matchesHyphenRange(lower, upper, resolved) {
				return true
			}
			continue
		}
		matched := true
		for _, token := range versionRangeTokens(alternative) {
			if !matchesRangeToken(token, resolved) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func matchesHyphenRange(lower, upper string, actual []int) bool {
	minimum, minimumOK := numericVersion(lower)
	maximum, maximumOK := numericVersion(upper)
	if !minimumOK || !maximumOK || compareVersions(actual, minimum) < 0 {
		return false
	}
	if len(maximum) >= 3 {
		return compareVersions(actual, maximum) <= 0
	}
	maximum[len(maximum)-1]++
	return compareVersions(actual, maximum) < 0
}

func versionRangeTokens(requirement string) []string {
	fields := strings.Fields(strings.ReplaceAll(requirement, ",", " "))
	var tokens []string
	for index := 0; index < len(fields); index++ {
		switch fields[index] {
		case ">=", "<=", ">", "<", "=", "^", "~":
			if index+1 < len(fields) {
				tokens = append(tokens, fields[index]+fields[index+1])
				index++
				continue
			}
		}
		tokens = append(tokens, fields[index])
	}
	return tokens
}

func matchesRangeToken(token string, actual []int) bool {
	operator := ""
	for _, candidate := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(token, candidate) {
			operator, token = candidate, strings.TrimPrefix(token, candidate)
			break
		}
	}
	lowerToken := strings.ToLower(token)
	if lowerToken == "*" || lowerToken == "x" {
		return operator == "" || operator == "=" || operator == ">=" || operator == "<="
	}
	if strings.ContainsAny(lowerToken, "x*") {
		prefix := strings.TrimSuffix(strings.TrimSuffix(lowerToken, ".x"), ".*")
		required, ok := numericVersion(prefix)
		if !ok {
			return false
		}
		switch operator {
		case ">":
			return compareVersions(actual, nextVersionPrefix(required)) >= 0
		case ">=":
			return compareVersions(actual, required) >= 0
		case "<":
			return compareVersions(actual, required) < 0
		case "<=":
			return compareVersions(actual, nextVersionPrefix(required)) < 0
		case "", "=":
			return versionPartsPrefix(prefix, actual)
		default:
			return false
		}
	}
	required, ok := numericVersion(token)
	if !ok {
		return false
	}
	comparison := compareVersions(actual, required)
	switch operator {
	case ">=":
		return comparison >= 0
	case ">":
		if len(required) < 3 {
			return compareVersions(actual, nextVersionPrefix(required)) >= 0
		}
		return comparison > 0
	case "<=":
		if len(required) < 3 {
			return compareVersions(actual, nextVersionPrefix(required)) < 0
		}
		return comparison <= 0
	case "<":
		return comparison < 0
	case "^":
		upper := append([]int(nil), required...)
		pivot := len(upper) - 1
		for index, part := range upper {
			if part != 0 {
				pivot = index
				break
			}
		}
		upper[pivot]++
		for index := pivot + 1; index < len(upper); index++ {
			upper[index] = 0
		}
		return comparison >= 0 && compareVersions(actual, upper) < 0
	case "~":
		upper := append([]int(nil), required...)
		if len(upper) == 1 {
			upper[0]++
		} else {
			upper[1]++
			for index := 2; index < len(upper); index++ {
				upper[index] = 0
			}
		}
		return comparison >= 0 && compareVersions(actual, upper) < 0
	case "", "=":
		return versionPartsPrefix(token, actual)
	default:
		return false
	}
}

func nextVersionPrefix(version []int) []int {
	next := append([]int(nil), version...)
	next[len(next)-1]++
	return next
}

func versionPartsPrefix(requirement string, actual []int) bool {
	required, ok := numericVersion(requirement)
	if !ok || len(required) > len(actual) {
		return false
	}
	for index := range required {
		if required[index] != actual[index] {
			return false
		}
	}
	return true
}

func compareVersions(first, second []int) int {
	length := len(first)
	if len(second) > length {
		length = len(second)
	}
	for index := 0; index < length; index++ {
		var a, b int
		if index < len(first) {
			a = first[index]
		}
		if index < len(second) {
			b = second[index]
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
	}
	return 0
}

func runtimeDisplayName(name string) string {
	switch name {
	case "node":
		return "Node"
	case "go":
		return "Go"
	case "python":
		return "Python"
	case "rust":
		return "Rust"
	default:
		return name
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func uniqueStrings(values []string) []string {
	unique := make([]string, 0, len(values))
	for _, value := range values {
		unique = appendUnique(unique, value)
	}
	return unique
}

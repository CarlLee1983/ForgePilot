package repository

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestResolveRuntimeUsesInstalledNodeMatchingCandidateDeclaration(t *testing.T) {
	checkout := t.TempDir()
	writeRuntimeFixture(t, checkout, ".node-version", "24\n")
	writeRuntimeFixture(t, checkout, "Makefile", "ifneq ($(shell node --version),v24.8.0)\n$(error wrong Node runtime)\nendif\nverify:\n\t@test \"$$(node --version)\" = \"v24.8.0\"\n")

	wrong := filepath.Join(t.TempDir(), "bin")
	writeRuntimeExecutable(t, wrong, "node", "#!/bin/sh\necho v22.17.1\n")
	nvm := t.TempDir()
	matching := filepath.Join(nvm, "versions", "node", "v24.8.0", "bin")
	writeRuntimeExecutable(t, matching, "node", "#!/bin/sh\necho v24.8.0\n")
	t.Setenv("PATH", wrong+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NVM_DIR", nvm)

	runtime, err := ResolveRuntime(checkout)
	if err != nil {
		t.Fatal(err)
	}
	closeRuntime(t, runtime)
	if got, want := runtime.Versions(), map[string]string{"node": "24.8.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Versions() = %#v, want %#v", got, want)
	}
	if err := EnsureCanonicalCheck(checkout); err == nil {
		t.Fatal("canonical precheck unexpectedly accepted the caller's wrong Node")
	}
	if err := EnsureCanonicalCheckWithRuntime(checkout, runtime); err != nil {
		t.Fatalf("EnsureCanonicalCheckWithRuntime = %v", err)
	}

	log := new(strings.Builder)
	exitCode, err := RunCanonicalCheckWithRuntime(checkout, runtime, log)
	if err != nil || exitCode != 0 {
		t.Fatalf("RunCanonicalCheckWithRuntime = %d, %v", exitCode, err)
	}
}

func TestResolveRuntimeKeepsCurrentEnvironmentWithoutDeclarations(t *testing.T) {
	runtime, err := ResolveRuntime(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.Versions()) != 0 {
		t.Fatalf("Versions() = %#v, want none", runtime.Versions())
	}
}

func TestResolveRuntimeDiscoversSupportedDeclarations(t *testing.T) {
	tests := []struct {
		name, file, contents string
		want                 map[string]string
	}{
		{"nvmrc", ".nvmrc", "v24.8.0\n", map[string]string{"node": "24.8.0"}},
		{"tool versions", ".tool-versions", "nodejs 24.8.0\ngolang 1.25.5\npython 3.12.2\nrust 1.85.0\n", map[string]string{"node": "24.8.0", "go": "1.25.5", "python": "3.12.2", "rust": "1.85.0"}},
		{"mise", "mise.toml", "[tools]\nnode = \"24.8.0\"\ngo = \"1.25.5\"\npython = \"3.12.2\"\nrust = \"1.85.0\"\n", map[string]string{"node": "24.8.0", "go": "1.25.5", "python": "3.12.2", "rust": "1.85.0"}},
		{"package engines", "package.json", "{\"engines\":{\"node\":\">=24 <25\"}}\n", map[string]string{"node": "24.8.0"}},
		{"go mod", "go.mod", "module example.com/test\n\ngo 1.25.0\ntoolchain go1.25.5\n", map[string]string{"go": "1.25.5"}},
		{"python version", ".python-version", "3.12\n", map[string]string{"python": "3.12.2"}},
		{"rust toolchain toml", "rust-toolchain.toml", "[toolchain]\nchannel = \"1.85.0\"\n", map[string]string{"rust": "1.85.0"}},
		{"rust toolchain", "rust-toolchain", "1.85\n", map[string]string{"rust": "1.85.0"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checkout := t.TempDir()
			writeRuntimeFixture(t, checkout, test.file, test.contents)
			bin := filepath.Join(t.TempDir(), "bin")
			writeRuntimeExecutable(t, bin, "node", "#!/bin/sh\necho v24.8.0\n")
			writeRuntimeExecutable(t, bin, "go", "#!/bin/sh\necho 'go version go1.25.5 darwin/arm64'\n")
			writeRuntimeExecutable(t, bin, "python3", "#!/bin/sh\necho 'Python 3.12.2'\n")
			writeRuntimeExecutable(t, bin, "rustc", "#!/bin/sh\necho 'rustc 1.85.0 (test)'\n")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
			t.Setenv("NVM_DIR", t.TempDir())
			t.Setenv("MISE_DATA_DIR", t.TempDir())
			t.Setenv("ASDF_DATA_DIR", t.TempDir())
			t.Setenv("PYENV_ROOT", t.TempDir())
			runtime, err := ResolveRuntime(checkout)
			if err != nil {
				t.Fatal(err)
			}
			closeRuntime(t, runtime)
			if got := runtime.Versions(); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Versions() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResolveRuntimeRejectsConflictingDeclarations(t *testing.T) {
	checkout := t.TempDir()
	writeRuntimeFixture(t, checkout, ".node-version", "24\n")
	writeRuntimeFixture(t, checkout, ".nvmrc", "22\n")
	_, err := ResolveRuntime(checkout)
	if err == nil || !strings.Contains(err.Error(), "conflicting runtime declarations") {
		t.Fatalf("ResolveRuntime error = %v", err)
	}
}

func TestResolveRuntimeAcceptsCompatibleDeclarationsByPrecedence(t *testing.T) {
	checkout := t.TempDir()
	writeRuntimeFixture(t, checkout, "mise.toml", "[tools]\nnode = \"24\"\n")
	writeRuntimeFixture(t, checkout, ".tool-versions", "nodejs 24.8\n")
	writeRuntimeFixture(t, checkout, ".node-version", "24.8.0\n")
	writeRuntimeFixture(t, checkout, ".nvmrc", "v24\n")
	writeRuntimeFixture(t, checkout, "package.json", "{\"engines\":{\"node\":\">=24 <25\"}}\n")
	bin := filepath.Join(t.TempDir(), "bin")
	writeRuntimeExecutable(t, bin, "node", "#!/bin/sh\necho v24.8.0\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("NVM_DIR", t.TempDir())
	t.Setenv("MISE_DATA_DIR", t.TempDir())
	t.Setenv("ASDF_DATA_DIR", t.TempDir())

	runtime, err := ResolveRuntime(checkout)
	if err != nil {
		t.Fatal(err)
	}
	closeRuntime(t, runtime)
	if got := runtime.Versions()["node"]; got != "24.8.0" {
		t.Fatalf("resolved node = %q", got)
	}
}

func TestPackageEngineSupportsNPMRanges(t *testing.T) {
	tests := []struct {
		name, requirement, actual string
		available                 bool
	}{
		{"hyphen range", "20 - 24", "24.8.0", true},
		{"zero-major caret", "^0.2.3", "0.2.9", true},
		{"zero-major caret upper bound", "^0.2.3", "0.3.0", false},
		{"wildcard", "*", "24.8.0", true},
		{"partial greater excludes same major", ">20", "20.1.0", false},
		{"partial greater includes next major", ">20", "21.0.0", true},
		{"partial less equal includes same major", "<=20", "20.1.0", true},
		{"partial less equal excludes next major", "<=20", "21.0.0", false},
		{"x greater excludes same prefix", ">20.x", "20.9.0", false},
		{"x greater includes next prefix", ">20.x", "21.0.0", true},
		{"x greater equal includes prefix", ">=20.x", "20.0.0", true},
		{"x greater equal excludes lower prefix", ">=20.x", "19.9.0", false},
		{"x less excludes prefix", "<20.x", "20.0.0", false},
		{"x less includes lower prefix", "<20.x", "19.9.0", true},
		{"x less equal includes prefix", "<=20.x", "20.9.0", true},
		{"x less equal excludes next prefix", "<=20.x", "21.0.0", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checkout := t.TempDir()
			writeRuntimeFixture(t, checkout, "package.json", "{\"engines\":{\"node\":"+strconv.Quote(test.requirement)+"}}\n")
			bin := filepath.Join(t.TempDir(), "bin")
			writeRuntimeExecutable(t, bin, "node", "#!/bin/sh\necho v"+test.actual+"\n")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
			t.Setenv("NVM_DIR", t.TempDir())
			t.Setenv("MISE_DATA_DIR", t.TempDir())
			t.Setenv("ASDF_DATA_DIR", t.TempDir())
			runtime, err := ResolveRuntime(checkout)
			if test.available {
				if err != nil {
					t.Fatal(err)
				}
				closeRuntime(t, runtime)
				if got := runtime.Versions()["node"]; got != test.actual {
					t.Fatalf("resolved node = %q, want %q", got, test.actual)
				}
			} else if err == nil {
				_ = runtime.Close()
				t.Fatalf("accepted node %s for %s", test.actual, test.requirement)
			}
		})
	}
}

func TestResolveRuntimeRefusesUnavailableDeclaration(t *testing.T) {
	checkout := t.TempDir()
	writeRuntimeFixture(t, checkout, ".node-version", "99\n")
	bin := filepath.Join(t.TempDir(), "bin")
	writeRuntimeExecutable(t, bin, "node", "#!/bin/sh\necho v22.17.1\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("NVM_DIR", t.TempDir())
	_, err := ResolveRuntime(checkout)
	if err == nil || !strings.Contains(err.Error(), "Node 99 required") || !strings.Contains(err.Error(), "resolved 22.17.1") {
		t.Fatalf("ResolveRuntime error = %v", err)
	}
}

func TestResolveRuntimeBuildsOneEnvironmentForMultipleManagers(t *testing.T) {
	checkout := t.TempDir()
	writeRuntimeFixture(t, checkout, ".tool-versions", "nodejs 24.8.0\ngolang 1.25.5\npython 3.12.2\n")
	writeRuntimeFixture(t, checkout, "Makefile", "verify:\n\t@test \"$$(node --version)\" = v24.8.0\n\t@test \"$$(go version)\" = 'go version go1.25.5 darwin/arm64'\n\t@test \"$$(python3 --version)\" = 'Python 3.12.2'\n")

	nvm := t.TempDir()
	writeRuntimeExecutable(t, filepath.Join(nvm, "versions", "node", "v24.8.0", "bin"), "node", "#!/bin/sh\necho v24.8.0\n")
	mise := t.TempDir()
	goBin := filepath.Join(mise, "installs", "go", "1.25.5", "bin")
	writeRuntimeExecutable(t, goBin, "go", "#!/bin/sh\necho 'go version go1.25.5 darwin/arm64'\n")
	// The selected Go directory sorts before Node and deliberately contains a
	// wrong node. The private command directory must keep it from shadowing the
	// separately validated Node executable.
	writeRuntimeExecutable(t, goBin, "node", "#!/bin/sh\necho v20.0.0\n")
	asdf := t.TempDir()
	writeRuntimeExecutable(t, filepath.Join(asdf, "installs", "python", "3.12.2", "bin"), "python3", "#!/bin/sh\necho 'Python 3.12.2'\n")
	wrong := filepath.Join(t.TempDir(), "bin")
	writeRuntimeExecutable(t, wrong, "node", "#!/bin/sh\necho v22.17.1\n")
	writeRuntimeExecutable(t, wrong, "go", "#!/bin/sh\necho 'go version go1.24.0 darwin/arm64'\n")
	writeRuntimeExecutable(t, wrong, "python3", "#!/bin/sh\necho 'Python 3.9.6'\n")
	t.Setenv("PATH", wrong+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("NVM_DIR", nvm)
	t.Setenv("MISE_DATA_DIR", mise)
	t.Setenv("ASDF_DATA_DIR", asdf)

	runtime, err := ResolveRuntime(checkout)
	if err != nil {
		t.Fatal(err)
	}
	closeRuntime(t, runtime)
	if got, want := runtime.Summary(), "go 1.25.5, node 24.8.0, python 3.12.2"; got != want {
		t.Fatalf("Summary() = %q, want %q", got, want)
	}
	if exitCode, err := RunCanonicalCheckWithRuntime(checkout, runtime, new(strings.Builder)); err != nil || exitCode != 0 {
		t.Fatalf("canonical check = %d, %v", exitCode, err)
	}
}

func TestResolveRuntimeQueriesManagerShimFromCandidateCheckout(t *testing.T) {
	checkout := t.TempDir()
	writeRuntimeFixture(t, checkout, ".node-version", "24.8.0\n")
	bin := filepath.Join(t.TempDir(), "bin")
	writeRuntimeExecutable(t, bin, "node", "#!/bin/sh\nprintf 'v%s\\n' \"$(cat .node-version)\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("NVM_DIR", t.TempDir())
	t.Setenv("MISE_DATA_DIR", t.TempDir())
	t.Setenv("ASDF_DATA_DIR", t.TempDir())

	runtime, err := ResolveRuntime(checkout)
	if err != nil {
		t.Fatal(err)
	}
	closeRuntime(t, runtime)
	if got := runtime.Versions()["node"]; got != "24.8.0" {
		t.Fatalf("resolved node = %q, want candidate-local 24.8.0", got)
	}
}

func TestResolveRuntimeUsesInstalledNamedRustToolchain(t *testing.T) {
	checkout := t.TempDir()
	writeRuntimeFixture(t, checkout, "rust-toolchain", "stable\n")
	rustup := t.TempDir()
	writeRuntimeExecutable(t, filepath.Join(rustup, "toolchains", "stable-aarch64-apple-darwin", "bin"), "rustc", "#!/bin/sh\necho 'rustc 1.85.0 (test)'\n")
	t.Setenv("RUSTUP_HOME", rustup)
	t.Setenv("MISE_DATA_DIR", t.TempDir())
	t.Setenv("ASDF_DATA_DIR", t.TempDir())

	runtime, err := ResolveRuntime(checkout)
	if err != nil {
		t.Fatal(err)
	}
	closeRuntime(t, runtime)
	if got := runtime.Versions()["rust"]; got != "1.85.0" {
		t.Fatalf("resolved rust = %q, want stable toolchain 1.85.0", got)
	}
}

func TestResolveRuntimeUsesInstalledPyenvPython(t *testing.T) {
	checkout := t.TempDir()
	writeRuntimeFixture(t, checkout, ".python-version", "3.12\n")
	pyenv := t.TempDir()
	writeRuntimeExecutable(t, filepath.Join(pyenv, "versions", "3.12.2", "bin"), "python3", "#!/bin/sh\necho 'Python 3.12.2'\n")
	wrong := filepath.Join(t.TempDir(), "bin")
	writeRuntimeExecutable(t, wrong, "python3", "#!/bin/sh\necho 'Python 3.9.6'\n")
	t.Setenv("PATH", wrong+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("PYENV_ROOT", pyenv)
	t.Setenv("MISE_DATA_DIR", t.TempDir())
	t.Setenv("ASDF_DATA_DIR", t.TempDir())

	runtime, err := ResolveRuntime(checkout)
	if err != nil {
		t.Fatal(err)
	}
	closeRuntime(t, runtime)
	if got := runtime.Versions()["python"]; got != "3.12.2" {
		t.Fatalf("resolved python = %q, want pyenv 3.12.2", got)
	}
}

func writeRuntimeFixture(t *testing.T, root, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func closeRuntime(t *testing.T, runtime RuntimeEnvironment) {
	t.Helper()
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close runtime environment: %v", err)
		}
	})
}

func writeRuntimeExecutable(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0755); err != nil {
		t.Fatal(err)
	}
}

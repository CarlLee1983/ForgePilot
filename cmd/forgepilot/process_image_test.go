package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalProcessImageResolvesStableLinkOnce(t *testing.T) {
	target := filepath.Join(t.TempDir(), "versions", "generation", "bin", "forgepilot")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("image"), 0700); err != nil {
		t.Fatal(err)
	}
	stable := filepath.Join(t.TempDir(), "forgepilot")
	if err := os.Symlink(target, stable); err != nil {
		t.Fatal(err)
	}
	got, reexec, err := canonicalProcessImage(stable)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || !reexec {
		t.Fatalf("canonical process image = %q, reexec=%t", got, reexec)
	}
}

func TestReexecProcessImagePassesCanonicalImageArgsAndEnvironment(t *testing.T) {
	target := filepath.Join(t.TempDir(), "versions", "generation", "bin", "forgepilot")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("image"), 0700); err != nil {
		t.Fatal(err)
	}
	stable := filepath.Join(t.TempDir(), "forgepilot")
	if err := os.Symlink(target, stable); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"forgepilot", "status"}
	environment := []string{"HOME=/example", "PATH=/example/bin"}
	sentinel := errors.New("intercepted exec")
	err = reexecProcessImage(stable, args, environment, func(path string, gotArgs, gotEnvironment []string) error {
		if path != want || len(gotArgs) != len(args) || len(gotEnvironment) != len(environment) ||
			gotArgs[1] != "status" || gotEnvironment[0] != "HOME=/example" {
			t.Fatalf("exec path=%q args=%q environment=%q", path, gotArgs, gotEnvironment)
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("reexec error = %v, want sentinel", err)
	}
}

func TestRequiresRunnerGenerationOnlyForMutatingRunnerCommands(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		want      bool
	}{
		{nil, false},
		{[]string{"status"}, false},
		{[]string{"run"}, false},
		{[]string{"run", "status", "run-1"}, false},
		{[]string{"run", "--goal", "g", "--runtime", "fake", "--snapshot", "--dry-run"}, false},
		{[]string{"run", "resume", "run-1"}, true},
		{[]string{"run", "--goal", "g", "--runtime", "fake", "--snapshot"}, true},
		{[]string{"execution", "plan", "--request", "request.json", "--json"}, false},
		{[]string{"execution", "resume", "--goal", "g"}, true},
	} {
		if got := requiresRunnerGeneration(test.arguments); got != test.want {
			t.Errorf("requiresRunnerGeneration(%q) = %t, want %t", test.arguments, got, test.want)
		}
	}
}

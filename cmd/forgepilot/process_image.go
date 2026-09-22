package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// reexecCanonicalProcessImage makes a managed stable link select one exact
// generation before any ForgePilot command runs. A later Bootstrap current
// switch cannot retarget this process image. A source/development executable
// that is not a symlink continues unchanged and is rejected only if a managed
// generation is later required.
func reexecCanonicalProcessImage() error {
	startupPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("discover process image: %w", err)
	}
	return reexecProcessImage(startupPath, os.Args, os.Environ(), syscall.Exec)
}

func reexecProcessImage(startupPath string, args, environment []string,
	execProcess func(string, []string, []string) error) error {
	canonicalPath, reexec, err := canonicalProcessImage(startupPath)
	if err != nil {
		return err
	}
	if !reexec {
		return nil
	}
	return execProcess(canonicalPath, args, environment)
}

func canonicalProcessImage(startupPath string) (string, bool, error) {
	if !filepath.IsAbs(startupPath) || filepath.Clean(startupPath) != startupPath {
		return "", false, fmt.Errorf("process image is not an absolute clean path")
	}
	canonicalPath, err := filepath.EvalSymlinks(startupPath)
	if err != nil {
		return "", false, fmt.Errorf("resolve process image: %w", err)
	}
	if !filepath.IsAbs(canonicalPath) || filepath.Clean(canonicalPath) != canonicalPath {
		return "", false, fmt.Errorf("canonical process image is not an absolute clean path")
	}
	return canonicalPath, canonicalPath != startupPath, nil
}

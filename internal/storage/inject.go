package storage

import (
	"path/filepath"
	"sync/atomic"
)

// This file exists for the same reason internal/process/inject.go does: the
// failure the code around it must survive — a state transaction that fails
// after the work it was recording already happened — cannot be asked of a real
// filesystem at a chosen instant. Filling the disk, revoking permissions on
// .forgepilot or deleting state.json would all break the run record too, and a
// test built on any of them proves only that a corrupted workspace is refused.
//
// The seam is deliberately out of reach: an internal package, a function rather
// than an environment variable, keyed to one workspace's state.json, armed for
// exactly one write, and consulted nowhere except immediately before that one
// atomic replacement. There is no flag, setting or bypass in the CLI that can
// reach it, and in every real run the check is one atomic load that finds nil.
type armedSaveFailure struct {
	path string
	fail func() error
}

var injectedSaveFailurePointer atomic.Pointer[armedSaveFailure]
var injectedDirectorySyncFailurePointer atomic.Pointer[armedSaveFailure]

// InjectStateSaveFailure makes the next atomic replacement of root's state.json
// fail with whatever fail returns, before the rename, and then disarms itself.
// Only that one file in that one workspace is affected: a run record written in
// the same moment still lands, which is what lets a test tell "the state was
// not saved" apart from "the workspace was destroyed".
//
// It is one-shot because the writes that follow one failure are the ones under
// test — the recovery that must still be recorded, and the next process that
// must still be able to read it. The returned function clears the arming rather
// than restoring whatever preceded it, so overlapping armings are not supported
// and tests that share this seam must not run in parallel. It must never be
// called from production code.
func InjectStateSaveFailure(root string, fail func() error) func() {
	armed := &armedSaveFailure{path: statePath(filepath.Join(canonicalOrAbsolute(root), stateDirectory)), fail: fail}
	injectedSaveFailurePointer.Store(armed)
	return func() { injectedSaveFailurePointer.CompareAndSwap(armed, nil) }
}

// injectedSaveFailure reports the armed failure when destination is the file it
// was armed for, disarming it before it is reported so a single arming can
// never fail two writes.
func injectedSaveFailure(destination string) error {
	armed := injectedSaveFailurePointer.Load()
	if armed == nil || armed.path != destination {
		return nil
	}
	if !injectedSaveFailurePointer.CompareAndSwap(armed, nil) {
		return nil
	}
	return armed.fail()
}

// InjectRunDirectorySyncFailure fails one Run Record write after its rename but
// before its parent directory is synced. The new bytes may be readable, yet
// callers must not treat them as durable until a later explicit sync succeeds.
// This is test-only and never used by production entry points.
func InjectRunDirectorySyncFailure(root, runID string, fail func() error) func() {
	path := filepath.Join(canonicalOrAbsolute(root), stateDirectory, runDirectory, runID, "run.json")
	armed := &armedSaveFailure{path: path, fail: fail}
	injectedDirectorySyncFailurePointer.Store(armed)
	return func() { injectedDirectorySyncFailurePointer.CompareAndSwap(armed, nil) }
}

func injectedDirectorySyncFailure(destination string) error {
	armed := injectedDirectorySyncFailurePointer.Load()
	if armed == nil || armed.path != destination {
		return nil
	}
	if !injectedDirectorySyncFailurePointer.CompareAndSwap(armed, nil) {
		return nil
	}
	return armed.fail()
}

// canonicalOrAbsolute resolves root the way every other entry point does, and
// falls back to the absolute path when it cannot: arming a seam is not a place
// to fail a test with an error about symlinks.
func canonicalOrAbsolute(root string) string {
	if canonical, err := canonicalRoot(root); err == nil {
		return canonical
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return root
	}
	return absolute
}

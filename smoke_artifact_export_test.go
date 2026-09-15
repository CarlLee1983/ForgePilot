package forgepilot_test

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SmokeArtifactVariable names a directory, outside the disposable fixture, into
// which one smoke run's evidence is copied before the fixture is deleted.
//
// It is a test-only setting, not a product CLI flag, and it is deliberately
// separate from FORGEPILOT_CODEX_SMOKE: asking for evidence must never be the
// thing that starts spending model quota. With the variable unset no test
// writes anywhere outside its own temporary directory.
const SmokeArtifactVariable = "FORGEPILOT_SMOKE_ARTIFACT_DIR"

// artifactEntry is one exported file in the manifest. The identifiers are what
// makes a copied file answerable — "result.json" alone says nothing about which
// attempt on which Work Item produced it.
type artifactEntry struct {
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	RunID      string `json:"run_id,omitempty"`
	WorkItemID string `json:"work_item_id,omitempty"`
	Attempt    int    `json:"attempt,omitempty"`
}

// artifactGap is something the export expected and did not get. It is a first
// class manifest record rather than a log line, because an export that quietly
// omits a file reads afterwards exactly like a run that never produced one.
type artifactGap struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// artifactExport writes one round's evidence into its own fresh directory.
//
// Two properties are the point of the type. It never writes into a directory
// that already exists, so a second round cannot overwrite the first. And every
// failure is retained: copy errors accumulate into failures and surface from
// close, so an export that lost half its files cannot end with a success
// message. Gaps (a source that was never produced) are recorded separately from
// failures (an export that went wrong), because they mean different things to
// whoever reads the manifest.
type artifactExport struct {
	dir      string
	entries  []artifactEntry
	missing  []artifactGap
	failures []string
}

// openArtifactExport creates this round's directory under base. The name
// carries a timestamp and a random suffix and is created with os.Mkdir, so two
// rounds started in the same second still cannot collide, and an existing
// directory is an error rather than a silent merge.
func openArtifactExport(base, name string, at time.Time) (*artifactExport, error) {
	if base == "" {
		return nil, errors.New("artifact export needs a destination directory")
	}
	if !filepath.IsAbs(base) {
		return nil, fmt.Errorf("artifact export destination %q must be an absolute path", base)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact base %s: %w", base, err)
	}
	suffix := make([]byte, 3)
	if _, err := cryptorand.Read(suffix); err != nil {
		return nil, err
	}
	directory := filepath.Join(base, fmt.Sprintf("%s-%s-%s",
		name, at.UTC().Format("20060102T150405"), hex.EncodeToString(suffix)))
	if err := os.Mkdir(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact directory %s: %w", directory, err)
	}
	return &artifactExport{dir: directory}, nil
}

// write stores one generated document — the environment record, the captured
// runner output — under the export.
func (export *artifactExport) write(relative string, contents []byte) {
	path, err := export.destination(relative)
	if err != nil {
		export.fail(relative, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		export.fail(relative, err)
		return
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		export.fail(relative, err)
		return
	}
	export.entries = append(export.entries, describe(relative, contents))
}

// copyFile copies one source file. A source that does not exist is a gap; any
// other error is a failure.
func (export *artifactExport) copyFile(relative, source string) {
	contents, err := os.ReadFile(source)
	if errors.Is(err, fs.ErrNotExist) {
		export.gap(relative, "the source file was never produced: "+source)
		return
	}
	if err != nil {
		export.fail(relative, err)
		return
	}
	export.write(relative, contents)
}

// copyTree copies a directory recursively. skip names sources that must not
// leave the fixture; a skipped path is recorded as a gap with its reason, so
// the manifest says the omission was deliberate.
func (export *artifactExport) copyTree(relative, source string, skip func(relative string) string) {
	info, err := os.Stat(source)
	if errors.Is(err, fs.ErrNotExist) {
		export.gap(relative, "the source directory was never produced: "+source)
		return
	}
	if err != nil {
		export.fail(relative, err)
		return
	}
	if !info.IsDir() {
		export.copyFile(relative, source)
		return
	}
	walkErr := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			export.fail(relative, err)
			return nil
		}
		within, err := filepath.Rel(source, path)
		if err != nil {
			export.fail(relative, err)
			return nil
		}
		if within == "." {
			return nil
		}
		target := filepath.ToSlash(filepath.Join(relative, within))
		// A skipped directory is refused once, by name, instead of once per file
		// inside it: a manifest listing every object in a Git directory as a gap
		// would bury the gaps that matter.
		if skip != nil {
			if reason := skip(filepath.ToSlash(within)); reason != "" {
				export.gap(target, reason)
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		if entry.IsDir() {
			return nil
		}
		// A symlink or device node would copy as something other than its own
		// bytes, so it is named rather than silently flattened.
		if !entry.Type().IsRegular() {
			export.gap(target, "not a regular file: "+entry.Type().String())
			return nil
		}
		export.copyFile(target, path)
		return nil
	})
	if walkErr != nil {
		export.fail(relative, walkErr)
	}
}

// gap records something the export expected and did not find.
func (export *artifactExport) gap(relative, reason string) {
	export.missing = append(export.missing, artifactGap{Path: filepath.ToSlash(relative), Reason: reason})
}

func (export *artifactExport) fail(relative string, err error) {
	export.failures = append(export.failures, fmt.Sprintf("%s: %v", filepath.ToSlash(relative), err))
}

// destination refuses a relative path that would escape the export directory,
// so a path assembled from a Work Item id or a walked tree cannot write outside
// the round's own folder.
func (export *artifactExport) destination(relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("invalid artifact path %q", relative)
	}
	cleaned := filepath.Clean(filepath.FromSlash(relative))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid artifact path %q", relative)
	}
	return filepath.Join(export.dir, cleaned), nil
}

// attemptPath recognises the session artifacts a run writes, so the manifest
// can say which attempt on which Work Item a file belongs to.
var attemptPath = regexp.MustCompile(`(?:^|/)runs/([^/]+)(?:/(wi-[0-9]+)-attempt-([0-9]+))?(?:/|$)`)

func describe(relative string, contents []byte) artifactEntry {
	digest := sha256.Sum256(contents)
	entry := artifactEntry{
		Path:   filepath.ToSlash(relative),
		Bytes:  int64(len(contents)),
		SHA256: hex.EncodeToString(digest[:]),
	}
	if match := attemptPath.FindStringSubmatch(entry.Path); match != nil {
		entry.RunID = match[1]
		if match[2] != "" {
			entry.WorkItemID = strings.ToUpper(match[2])
			entry.Attempt, _ = strconv.Atoi(match[3])
		}
	}
	return entry
}

// artifactManifest is the index of one round. Complete is the single field a
// reader should look at first: it is false whenever anything failed or was
// missing, so a partial export cannot be mistaken for a whole one.
type artifactManifest struct {
	Round     string          `json:"round"`
	WrittenAt string          `json:"written_at"`
	Complete  bool            `json:"complete"`
	Files     []artifactEntry `json:"files"`
	Missing   []artifactGap   `json:"missing,omitempty"`
	Failures  []string        `json:"failures,omitempty"`
}

// close writes the manifest and reports whether anything went wrong. The
// manifest is written even when the export failed — a record of what was lost
// is worth more than no record — and the error is returned all the same.
func (export *artifactExport) close(at time.Time) error {
	sort.Slice(export.entries, func(i, j int) bool { return export.entries[i].Path < export.entries[j].Path })
	sort.Slice(export.missing, func(i, j int) bool { return export.missing[i].Path < export.missing[j].Path })
	manifest := artifactManifest{
		Round:     filepath.Base(export.dir),
		WrittenAt: at.UTC().Format(time.RFC3339Nano),
		Complete:  len(export.failures) == 0 && len(export.missing) == 0,
		Files:     export.entries,
		Missing:   export.missing,
		Failures:  export.failures,
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	writeErr := os.WriteFile(filepath.Join(export.dir, "manifest.json"), append(encoded, '\n'), 0o600)
	if len(export.failures) > 0 {
		return fmt.Errorf("the evidence export did not complete; %d file(s) failed: %s",
			len(export.failures), strings.Join(export.failures, "; "))
	}
	return writeErr
}

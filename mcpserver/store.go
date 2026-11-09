package mcpserver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ahmedhodiani/gomontage/project"
)

// HistoryDir is the directory, next to a project file, that holds the
// snapshots written before every edit: <dir>/.gomontage/history/<file>/.
const HistoryDir = ".gomontage/history"

// store caches loaded projects by absolute path. An entry is reloaded when
// the file's modification time or size changes on disk, so edits made by
// other programs (or another server) are picked up, and every entry has its
// own mutex: tool calls on one project run one at a time, calls on different
// projects in parallel.
type store struct {
	mu      sync.Mutex
	entries map[string]*entry
}

// entry is one project file. Its fields are guarded by mu, which a tool
// call holds from open to release.
type entry struct {
	mu   sync.Mutex
	path string // absolute

	modTime time.Time
	size    int64
	data    []byte // the file's bytes when loaded

	proj     *project.Project // nil when the file is invalid
	warnings []project.Warning
	loadErr  error // why proj is nil
}

func newStore() *store { return &store{entries: map[string]*entry{}} }

// absPath makes path absolute (relative paths are relative to the server's
// working directory) and cleans it.
func absPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// open locks the project at path and returns it, (re)loaded from disk if it
// changed. The caller must call e.mu.Unlock (via release). A file that
// exists but is invalid is returned with proj == nil and loadErr set.
func (s *store) open(path string) (*entry, error) {
	abs, err := absPath(path)
	if err != nil {
		return nil, fmt.Errorf("project: %w", err)
	}
	s.mu.Lock()
	e, ok := s.entries[abs]
	if !ok {
		e = &entry{path: abs}
		s.entries[abs] = e
	}
	s.mu.Unlock()

	e.mu.Lock()
	if err := e.refresh(); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	return e, nil
}

// create locks the entry for a file that may not exist yet (project_create).
// If it exists, it is loaded (e.data is set) so a save snapshots it.
func (s *store) create(abs string) (*entry, error) {
	s.mu.Lock()
	e, ok := s.entries[abs]
	if !ok {
		e = &entry{path: abs}
		s.entries[abs] = e
	}
	s.mu.Unlock()
	e.mu.Lock()
	e.proj, e.data, e.loadErr = nil, nil, nil
	if _, err := os.Stat(abs); err == nil {
		if err := e.refresh(); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	}
	return e, nil
}

func (e *entry) release() { e.mu.Unlock() }

// refresh reloads the file if it changed since it was loaded.
func (e *entry) refresh() error {
	st, err := os.Stat(e.path)
	if err != nil {
		e.proj, e.data = nil, nil
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("project file not found: %s (create it with project_create)", e.path)
		}
		return fmt.Errorf("project: %w", err)
	}
	if st.IsDir() {
		return fmt.Errorf("project path is a directory: %s", e.path)
	}
	if e.data != nil && st.ModTime().Equal(e.modTime) && st.Size() == e.size {
		return nil
	}
	data, err := os.ReadFile(e.path)
	if err != nil {
		return fmt.Errorf("project: %w", err)
	}
	e.modTime, e.size, e.data = st.ModTime(), st.Size(), data
	e.proj, e.warnings, e.loadErr = project.Parse(data, filepath.Dir(e.path))
	if e.loadErr != nil {
		var ve *project.ValidationError
		if !errors.As(e.loadErr, &ve) {
			return fmt.Errorf("project: %w", e.loadErr)
		}
		e.proj = nil
	}
	return nil
}

// valid returns the loaded project, or an error listing why the file is
// invalid (edits, previews and renders need a valid project).
func (e *entry) valid() (*project.Project, error) {
	if e.proj != nil {
		return e.proj, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "project %s is invalid; fix these first (timeline_get shows them too):", e.path)
	var ve *project.ValidationError
	if errors.As(e.loadErr, &ve) {
		for _, p := range ve.Problems {
			b.WriteString("\n  " + problemText(p))
		}
	} else if e.loadErr != nil {
		b.WriteString("\n  " + e.loadErr.Error())
	}
	return nil, errors.New(b.String())
}

// save writes p over the entry's file with the canonical project.Save
// (atomic), after snapshotting the current file into the history
// directory. The entry is updated to the saved state.
func (e *entry) save(p *project.Project) error {
	if e.data != nil {
		if _, err := writeSnapshot(e.path, e.data); err != nil {
			return err
		}
	}
	return e.write(p)
}

// write saves p without a snapshot (new files).
func (e *entry) write(p *project.Project) error {
	if err := project.Save(e.path, p); err != nil {
		return err
	}
	e.data = nil // force a reload of exactly what was written
	return e.refresh()
}

// writeSnapshot stores data as the next snapshot of the project file at
// path and returns the snapshot's path. Names sort by time:
// <UTC time>[-n].gomontage.json.
func writeSnapshot(path string, data []byte) (string, error) {
	dir := filepath.Join(filepath.Dir(path), filepath.FromSlash(HistoryDir), filepath.Base(path))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("history snapshot: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405.000Z")
	name := filepath.Join(dir, stamp+project.FileExtension)
	for n := 2; ; n++ {
		if _, err := os.Stat(name); errors.Is(err, os.ErrNotExist) {
			break
		}
		name = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stamp, n, project.FileExtension))
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		return "", fmt.Errorf("history snapshot: %w", err)
	}
	return name, nil
}

// sameFile reports whether a and b (absolute) name the same file, following
// symlinks when the files exist.
func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	sa, errA := os.Stat(a)
	sb, errB := os.Stat(b)
	if errA == nil && errB == nil {
		return os.SameFile(sa, sb)
	}
	ra, errA := filepath.EvalSymlinks(filepath.Dir(a))
	rb, errB := filepath.EvalSymlinks(filepath.Dir(b))
	if errA == nil && errB == nil {
		return filepath.Join(ra, filepath.Base(a)) == filepath.Join(rb, filepath.Base(b))
	}
	return false
}

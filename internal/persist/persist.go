// Package persist saves the session to a file and restores it at startup, so
// a restart (deployment, node drain, crash) costs students a few seconds of
// reconnection instead of the whole session.
//
// The state is small (a class, a few hours) and the process is alone
// (one replica): a JSON snapshot written atomically beats a database here.
package persist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// fileVersion is the envelope's format; each part versions its own content.
const fileVersion = 1

// Part is a piece of state that can be saved: the classroom, teacher sessions.
type Part interface {
	// Revision changes whenever the state changes.
	Revision() int64
	// MarshalState returns the state and the revision it reflects.
	MarshalState() ([]byte, int64, error)
	RestoreState(data []byte) error
}

type file struct {
	Version int                        `json:"version"`
	SavedAt time.Time                  `json:"savedAt"`
	Parts   map[string]json.RawMessage `json:"parts"`
}

// Saver writes the parts to one file when they change.
type Saver struct {
	path  string
	parts map[string]Part
	log   *slog.Logger

	mu    sync.Mutex // one save at a time
	saved map[string]int64
}

// New prepares a saver for path; nothing is read or written yet.
func New(path string, log *slog.Logger, parts map[string]Part) *Saver {
	return &Saver{path: path, parts: parts, log: log, saved: map[string]int64{}}
}

// Load restores every part found in the file. A missing file is a first
// start. An unreadable file is set aside (renamed) and the session starts
// empty: a class must never be blocked by a broken file.
func (s *Saver) Load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		s.log.Info("aucune séance sauvegardée", "file", s.path)
		return nil
	}
	if err != nil {
		return fmt.Errorf("lecture de %s : %w", s.path, err)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return s.setAside(fmt.Errorf("JSON invalide : %w", err))
	}
	if f.Version != fileVersion {
		return s.setAside(fmt.Errorf("format inconnu (version %d, attendue %d)", f.Version, fileVersion))
	}
	for name, p := range s.parts {
		raw, ok := f.Parts[name]
		if !ok {
			continue
		}
		if err := p.RestoreState(raw); err != nil {
			return s.setAside(fmt.Errorf("%s : %w", name, err))
		}
		s.saved[name] = p.Revision() // restored = already on disk
	}
	s.log.Info("séance restaurée", "file", s.path, "savedAt", f.SavedAt)
	return nil
}

func (s *Saver) setAside(cause error) error {
	aside := fmt.Sprintf("%s.illisible-%s", s.path, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.Rename(s.path, aside); err != nil {
		return fmt.Errorf("état illisible (%w) et impossible à mettre de côté : %w", cause, err)
	}
	s.log.Error("état illisible mis de côté, nouvelle séance", "err", cause, "file", aside)
	return nil
}

// Probe checks that the state can be written, by writing and removing a
// file next to it: better to fail at startup than in the middle of a class.
func (s *Saver) Probe() error {
	f, err := os.CreateTemp(filepath.Dir(s.path), ".probe-*.tmp")
	if err != nil {
		return fmt.Errorf("écriture impossible dans %s : %w", filepath.Dir(s.path), err)
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// Run saves every interval while something changed, until ctx is done.
// Call Save once more after the last change (shutdown).
func (s *Saver) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Save(); err != nil {
				s.log.Error("sauvegarde de la séance", "err", err)
			}
		}
	}
}

// Save writes the file if any part changed since the last save.
func (s *Saver) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for name, p := range s.parts {
		if rev, ok := s.saved[name]; !ok || rev != p.Revision() {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	f := file{Version: fileVersion, SavedAt: time.Now().UTC(), Parts: map[string]json.RawMessage{}}
	revs := map[string]int64{}
	for name, p := range s.parts {
		data, rev, err := p.MarshalState()
		if err != nil {
			return fmt.Errorf("%s : %w", name, err)
		}
		f.Parts[name] = data
		revs[name] = rev
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := writeAtomic(s.path, data); err != nil {
		return err
	}
	for name, rev := range revs {
		s.saved[name] = rev
	}
	return nil
}

// writeAtomic replaces path with data: a crash leaves the old file or the
// new one, never half of one.
func writeAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("fichier temporaire dans %s : %w", dir, err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil { // pseudos and answers of students
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	// the rename itself must reach the disk
	d, err := os.Open(dir) //nolint:gosec // G304: dir is the configured state directory
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

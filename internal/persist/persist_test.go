package persist

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"audience-keeper/internal/classroom"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// counting wraps a part to count saves.
type counting struct {
	Part
	mu    sync.Mutex
	saves int
}

func (c *counting) MarshalState() ([]byte, int64, error) {
	c.mu.Lock()
	c.saves++
	c.mu.Unlock()
	return c.Part.MarshalState()
}

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	room := classroom.New(classroom.WithCode("ABC123"))
	st, token, err := room.Join("ABC123", "Léa")
	if err != nil {
		t.Fatal(err)
	}
	cp := &counting{Part: room}
	s := New(path, quiet(), map[string]Part{"classroom": cp})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil || cp.saves != 1 {
		t.Fatalf("nothing changed: no second write (%d writes, %v)", cp.saves, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file must be private: %v %v", info.Mode(), err)
	}

	restored := classroom.New()
	if err := New(path, quiet(), map[string]Part{"classroom": restored}).Load(); err != nil {
		t.Fatal(err)
	}
	if id, ok := restored.Authenticate(token); !ok || id != st.ID || restored.Code() != "ABC123" {
		t.Fatal("session not restored")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".session-*")); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestLoadMissingFileIsAFirstStart(t *testing.T) {
	room := classroom.New()
	code := room.Code()
	if err := New(filepath.Join(t.TempDir(), "absent.json"), quiet(), map[string]Part{"classroom": room}).Load(); err != nil {
		t.Fatal(err)
	}
	if room.Code() != code {
		t.Fatal("nothing to restore: the new session stays as is")
	}
}

func TestUnreadableFileIsSetAside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	for name, content := range map[string]string{
		"garbage":    "{pas du json",
		"bad part":   `{"version":1,"parts":{"classroom":{"version":1,"code":"X","answers":[{"questionId":"q9","studentId":"s9"}]}}}`,
		"future fmt": `{"version":7,"parts":{}}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		room := classroom.New()
		if err := New(path, quiet(), map[string]Part{"classroom": room}).Load(); err != nil {
			t.Fatalf("%s: a broken file must not stop the server: %v", name, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s: the broken file must be moved away", name)
		}
	}
	aside, _ := filepath.Glob(filepath.Join(dir, "session.json.illisible-*"))
	if len(aside) == 0 {
		t.Fatal("the broken file must be kept for inspection")
	}
}

func TestRunSavesChangesUntilCancelled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	room := classroom.New(classroom.WithCode("ABC123"))
	s := New(path, quiet(), map[string]Part{"classroom": room})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx, 10*time.Millisecond); close(done) }()

	if _, _, err := room.Join("ABC123", "Tom"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, _ := os.ReadFile(path) //nolint:gosec // G304: fichier du répertoire temporaire du test
		if strings.Contains(string(data), "Tom") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("change not saved in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestSaveFailsLoudlyOnUnwritableDir(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "absent", "session.json"), quiet(), map[string]Part{"classroom": classroom.New()})
	if err := s.Save(); err == nil {
		t.Fatal("a save that cannot happen must be reported")
	}
	if err := s.Probe(); err == nil {
		t.Fatal("an unwritable directory must be caught at startup")
	}
	if err := New(filepath.Join(t.TempDir(), "session.json"), quiet(), nil).Probe(); err != nil {
		t.Fatalf("writable directory: %v", err)
	}
}

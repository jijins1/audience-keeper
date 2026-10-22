package classroom

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Classroom is the in-memory state of the current session. It is safe for
// concurrent use. Accessors return copies so callers never share internal state.
type Classroom struct {
	mu       sync.RWMutex
	notifier Notifier
	now      func() time.Time

	code        string
	startedAt   time.Time
	aiMode      AIMode
	seq         int64
	nextStudent int
	nextQ       int
	nextID      int

	students    map[string]*Student
	studentList []string          // join order
	pseudos     map[string]bool   // lowercased pseudos
	tokens      map[string]string // token -> student id
	conns       map[string]int    // student id -> open connections

	questions []*Question
	byQID     map[string]*Question
	answers   map[string]map[string]*Answer // question id -> student id -> answer

	events      []Event
	alerts      []Alert
	alertKeys   map[string]bool
	suggestions []*Suggestion
	messages    []Message

	slide     *LiveSlide
	presenter *PresenterInfo
	slideRev  int64

	rev int64 // bumped on every change, see Revision
}

// Option configures a Classroom.
type Option func(*Classroom)

// WithNotifier sets the change notifier.
func WithNotifier(n Notifier) Option { return func(c *Classroom) { c.notifier = n } }

// WithClock overrides the clock (tests).
func WithClock(now func() time.Time) Option { return func(c *Classroom) { c.now = now } }

// WithCode forces the join code (tests).
func WithCode(code string) Option { return func(c *Classroom) { c.code = code } }

// New creates a classroom with a fresh join code.
func New(opts ...Option) *Classroom {
	c := &Classroom{notifier: nopNotifier{}, now: time.Now}
	for _, o := range opts {
		o(c)
	}
	forced := c.code
	c.resetLocked()
	if forced != "" {
		c.code = forced
	}
	return c
}

// SetNotifier replaces the notifier; used once at wiring time.
func (c *Classroom) SetNotifier(n Notifier) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifier = n
}

func (c *Classroom) resetLocked() {
	c.code = newCode()
	c.startedAt = c.now()
	if c.aiMode == "" {
		c.aiMode = AIModeSuggest
	}
	// IDs and sequence numbers stay monotonic across resets so stale
	// connections and MCP cursors can never refer to new objects.
	c.students = map[string]*Student{}
	c.studentList = nil
	c.pseudos = map[string]bool{}
	c.tokens = map[string]string{}
	c.conns = map[string]int{}
	c.questions = nil
	c.byQID = map[string]*Question{}
	c.answers = map[string]map[string]*Answer{}
	c.events = nil
	c.alerts = nil
	c.alertKeys = map[string]bool{}
	c.suggestions = nil
	c.messages = nil
	c.slide = nil
	c.presenter = nil
}

// Reset starts a brand new session: everything is forgotten and a new code issued.
func (c *Classroom) Reset() {
	c.mu.Lock()
	c.resetLocked()
	c.rev++
	n := c.notifier
	c.mu.Unlock()
	n.TeacherDirty()
	n.StudentsDirty()
}

// Code returns the join code.
func (c *Classroom) Code() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.code
}

// StartedAt returns when the session started.
func (c *Classroom) StartedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.startedAt
}

// mutate runs fn under the write lock, then notifies outside of it.
// fn returns the student IDs to refresh (nil = none, allStudents = everyone).
func (c *Classroom) mutate(fn func() (refresh []string, err error)) error {
	c.mu.Lock()
	refresh, err := fn()
	c.rev++ // also on error: fn may have changed state before failing
	n := c.notifier
	c.mu.Unlock()
	if err != nil {
		return err
	}
	n.TeacherDirty()
	switch {
	case refresh == nil:
	case len(refresh) == 1 && refresh[0] == allStudents:
		n.StudentsDirty()
	default:
		n.StudentsDirty(refresh...)
	}
	return nil
}

const allStudents = "*"

func everyone() []string { return []string{allStudents} }

// Join registers a student and returns their ID and secret token.
func (c *Classroom) Join(code, pseudo string) (Student, string, error) {
	pseudo = strings.TrimSpace(pseudo)
	if !validPseudo(pseudo) {
		return Student{}, "", ErrBadPseudo
	}
	var st Student
	token := randomHex(24)
	err := c.mutate(func() ([]string, error) {
		if subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(code))), []byte(c.code)) != 1 {
			return nil, ErrBadCode
		}
		if len(c.students) >= MaxStudents {
			return nil, ErrFull
		}
		key := strings.ToLower(pseudo)
		if c.pseudos[key] {
			return nil, ErrPseudoTaken
		}
		c.pseudos[key] = true
		c.nextStudent++
		now := c.now()
		s := &Student{ID: fmt.Sprintf("s%d", c.nextStudent), Pseudo: pseudo, JoinedAt: now, LastSeen: now}
		c.students[s.ID] = s
		c.studentList = append(c.studentList, s.ID)
		c.tokens[hashToken(token)] = s.ID
		st = *s
		return nil, nil
	})
	if err != nil {
		return Student{}, "", err
	}
	return st, token, nil
}

// Authenticate resolves a student token.
func (c *Classroom) Authenticate(token string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, ok := c.tokens[hashToken(token)]
	return id, ok
}

// Connect marks a student connection as open.
func (c *Classroom) Connect(studentID string) error {
	return c.mutate(func() ([]string, error) {
		s, ok := c.students[studentID]
		if !ok {
			return nil, ErrUnknownStudent
		}
		c.conns[studentID]++
		if !s.Connected {
			s.Connected = true
			s.connectedAt = c.now()
			c.addEventLocked(Event{StudentID: studentID, Kind: EventJoin})
		}
		s.LastSeen = c.now()
		return nil, nil
	})
}

// Disconnect marks a student connection as closed.
func (c *Classroom) Disconnect(studentID string) {
	_ = c.mutate(func() ([]string, error) {
		s, ok := c.students[studentID]
		if !ok {
			return nil, ErrUnknownStudent
		}
		if c.conns[studentID] > 0 {
			c.conns[studentID]--
		}
		if c.conns[studentID] == 0 && s.Connected {
			s.Connected = false
			c.addEventLocked(Event{StudentID: studentID, Kind: EventLeave})
		}
		s.LastSeen = c.now()
		return nil, nil
	})
}

// Students returns all students in join order.
func (c *Classroom) Students() []Student {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.studentsLocked()
}

func (c *Classroom) studentsLocked() []Student {
	out := make([]Student, 0, len(c.studentList))
	for _, id := range c.studentList {
		out = append(out, *c.students[id])
	}
	return out
}

func (c *Classroom) nextSeqLocked() int64 {
	c.seq++
	return c.seq
}

func (c *Classroom) newIDLocked(prefix string) string {
	c.nextID++
	return fmt.Sprintf("%s%d", prefix, c.nextID)
}

func (c *Classroom) addEventLocked(e Event) {
	e.Seq = c.nextSeqLocked()
	e.Time = c.now()
	c.events = append(c.events, e)
	if len(c.events) > 2*maxEvents { // trim in batches: amortised O(1)
		c.events = append([]Event(nil), c.events[len(c.events)-maxEvents:]...)
	}
}

func validPseudo(p string) bool {
	n := utf8.RuneCountInString(p)
	if n < 1 || n > MaxPseudoLen {
		return false
	}
	for _, r := range p {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

func validText(s string, max int) error {
	if utf8.RuneCountInString(s) > max {
		return ErrTooLong
	}
	if !utf8.ValidString(s) {
		return ErrBadAnswer
	}
	return nil
}

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newCode() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("crypto/rand: %w", err))
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("crypto/rand: %w", err))
	}
	return hex.EncodeToString(b)
}

package classroom

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// stateVersion is bumped whenever the saved format changes incompatibly.
const stateVersion = 1

// ErrBadState is returned when saved state cannot be restored.
var ErrBadState = errors.New("état sauvegardé illisible")

// state is the whole session as saved on disk. Student tokens are stored
// hashed; connections and the live slide are not saved (clients reconnect,
// the deck sends its slide again).
type state struct {
	Version     int               `json:"version"`
	Code        string            `json:"code"`
	StartedAt   time.Time         `json:"startedAt"`
	AIMode      AIMode            `json:"aiMode"`
	Seq         int64             `json:"seq"`
	NextStudent int               `json:"nextStudent"`
	NextQ       int               `json:"nextQ"`
	NextID      int               `json:"nextId"`
	Students    []Student         `json:"students"` // join order
	Tokens      map[string]string `json:"tokens"`   // sha256(token) -> student id
	Questions   []Question        `json:"questions"`
	Answers     []Answer          `json:"answers"`
	Events      []Event           `json:"events"`
	Alerts      []Alert           `json:"alerts"`
	AlertKeys   []string          `json:"alertKeys"`
	Suggestions []Suggestion      `json:"suggestions"`
	Messages    []Message         `json:"messages"`
}

// hashToken is how student tokens are kept, in memory and on disk: a leaked
// state file does not let anyone impersonate a student.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Revision changes whenever the session changes: a saver compares it with
// the revision it last wrote.
func (c *Classroom) Revision() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rev
}

// MarshalState serialises the session, with the revision it reflects.
func (c *Classroom) MarshalState() ([]byte, int64, error) {
	c.mu.RLock()
	s := state{
		Version: stateVersion, Code: c.code, StartedAt: c.startedAt, AIMode: c.aiMode, Seq: c.seq,
		NextStudent: c.nextStudent, NextQ: c.nextQ, NextID: c.nextID,
		Students:  c.studentsLocked(),
		Tokens:    make(map[string]string, len(c.tokens)),
		Questions: make([]Question, 0, len(c.questions)),
		Events:    append([]Event(nil), c.events...),
		Alerts:    append([]Alert(nil), c.alerts...),
		Messages:  append([]Message(nil), c.messages...),
	}
	for h, id := range c.tokens {
		s.Tokens[h] = id
	}
	for _, q := range c.questions {
		s.Questions = append(s.Questions, copyQuestion(q))
		s.Answers = append(s.Answers, c.answersLocked(q.ID)...)
	}
	for k := range c.alertKeys {
		s.AlertKeys = append(s.AlertKeys, k)
	}
	for _, sg := range c.suggestions {
		s.Suggestions = append(s.Suggestions, *sg)
	}
	rev := c.rev
	c.mu.RUnlock()
	data, err := json.Marshal(s)
	return data, rev, err
}

// RestoreState replaces the session with a saved one. Every student starts
// disconnected; their pages reconnect with the token they kept.
func (c *Classroom) RestoreState(data []byte) error {
	var s state
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("%w: %w", ErrBadState, err)
	}
	if s.Version != stateVersion {
		return fmt.Errorf("%w: version %d (attendue %d)", ErrBadState, s.Version, stateVersion)
	}
	if err := s.check(); err != nil {
		return err
	}
	c.mu.Lock()
	c.resetLocked()
	c.code, c.startedAt, c.aiMode, c.seq = s.Code, s.StartedAt, s.AIMode, s.Seq
	c.nextStudent, c.nextQ, c.nextID = s.NextStudent, s.NextQ, s.NextID
	for i := range s.Students {
		st := s.Students[i]
		st.Connected = false
		c.students[st.ID] = &st
		c.studentList = append(c.studentList, st.ID)
		c.pseudos[strings.ToLower(st.Pseudo)] = true
	}
	for h, id := range s.Tokens {
		c.tokens[h] = id
	}
	for i := range s.Questions {
		q := copyQuestion(&s.Questions[i])
		c.questions = append(c.questions, &q)
		c.byQID[q.ID] = &q
		c.answers[q.ID] = map[string]*Answer{}
	}
	for i := range s.Answers {
		a := s.Answers[i]
		c.answers[a.QuestionID][a.StudentID] = &a
	}
	c.events, c.alerts, c.messages = s.Events, s.Alerts, s.Messages
	for _, k := range s.AlertKeys {
		c.alertKeys[k] = true
	}
	for i := range s.Suggestions {
		sg := s.Suggestions[i]
		c.suggestions = append(c.suggestions, &sg)
	}
	c.rev++
	n := c.notifier
	c.mu.Unlock()
	n.TeacherDirty()
	n.StudentsDirty()
	return nil
}

// check rejects a state whose references do not hold together, rather than
// restoring something that would panic later.
func (s *state) check() error {
	bad := func(what string) error { return fmt.Errorf("%w: %s", ErrBadState, what) }
	if len(s.Code) == 0 || len(s.Students) > MaxStudents {
		return bad("en-tête")
	}
	students := map[string]bool{}
	for _, st := range s.Students {
		if st.ID == "" || students[st.ID] {
			return bad("étudiant " + st.ID)
		}
		students[st.ID] = true
	}
	for _, id := range s.Tokens {
		if !students[id] {
			return bad("jeton d'un étudiant inconnu")
		}
	}
	questions := map[string]bool{}
	for _, q := range s.Questions {
		if q.ID == "" || questions[q.ID] {
			return bad("question " + q.ID)
		}
		questions[q.ID] = true
	}
	for _, a := range s.Answers {
		if !questions[a.QuestionID] || !students[a.StudentID] {
			return bad("réponse orpheline")
		}
	}
	return nil
}

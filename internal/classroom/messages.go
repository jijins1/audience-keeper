package classroom

import (
	"strings"
	"time"
)

// MaxAutoMessagesPerQuestion caps how many AI messages a student can receive
// per question in auto mode.
const MaxAutoMessagesPerQuestion = 2

// AIMode returns the current AI messaging mode.
func (c *Classroom) AIMode() AIMode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.aiMode
}

// SetAIMode switches between suggestion (teacher approves) and auto modes.
func (c *Classroom) SetAIMode(m AIMode) error {
	if m != AIModeSuggest && m != AIModeAuto {
		return ErrBadMode
	}
	return c.mutate(func() ([]string, error) {
		c.aiMode = m
		return nil, nil
	})
}

// SendMessage delivers a message to a student right away.
func (c *Classroom) SendMessage(sid, text string, fromAI bool) (Message, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Message{}, ErrEmpty
	}
	if err := validText(text, MaxMessageLen); err != nil {
		return Message{}, err
	}
	var out Message
	err := c.mutate(func() ([]string, error) {
		if _, ok := c.students[sid]; !ok {
			return nil, ErrUnknownStudent
		}
		out = c.sendLocked(sid, text, fromAI)
		return []string{sid}, nil
	})
	return out, err
}

func (c *Classroom) sendLocked(sid, text string, fromAI bool) Message {
	m := Message{ID: c.newIDLocked("m"), Time: c.now(), StudentID: sid, Text: text, FromAI: fromAI}
	c.messages = append(c.messages, m)
	return m
}

// SuggestMessage is how Claude talks to a student. In suggest mode the message
// waits for teacher approval; in auto mode it is sent within limits.
func (c *Classroom) SuggestMessage(sid, text string) (Suggestion, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Suggestion{}, ErrEmpty
	}
	if err := validText(text, MaxMessageLen); err != nil {
		return Suggestion{}, err
	}
	var out Suggestion
	err := c.mutate(func() ([]string, error) {
		if _, ok := c.students[sid]; !ok {
			return nil, ErrUnknownStudent
		}
		qid := ""
		if q := c.currentLocked(); q != nil {
			qid = q.ID
		}
		if c.aiMode == AIModeSuggest && c.pendingSuggestionsLocked() >= MaxPending {
			return nil, ErrTooManyPending
		}
		s := &Suggestion{ID: c.newIDLocked("sg"), Time: c.now(), StudentID: sid, QuestionID: qid, Text: text, Status: SuggestionPending}
		var refresh []string
		if c.aiMode == AIModeAuto {
			if c.autoSentLocked(sid, qid) >= MaxAutoMessagesPerQuestion {
				return nil, ErrRateLimited
			}
			c.sendLocked(sid, text, true)
			s.Status = SuggestionSent
			refresh = []string{sid}
		}
		c.suggestions = append(c.suggestions, s)
		out = *s
		return refresh, nil
	})
	return out, err
}

func (c *Classroom) pendingSuggestionsLocked() int {
	n := 0
	for _, s := range c.suggestions {
		if s.Status == SuggestionPending {
			n++
		}
	}
	return n
}

func (c *Classroom) autoSentLocked(sid, qid string) int {
	n := 0
	for _, s := range c.suggestions {
		if s.StudentID == sid && s.QuestionID == qid && s.Status == SuggestionSent {
			n++
		}
	}
	return n
}

// ApproveSuggestion sends a pending suggestion, optionally with edited text.
func (c *Classroom) ApproveSuggestion(id, editedText string) error {
	editedText = strings.TrimSpace(editedText)
	if err := validText(editedText, MaxMessageLen); err != nil {
		return err
	}
	return c.mutate(func() ([]string, error) {
		s := c.suggestionLocked(id)
		if s == nil {
			return nil, ErrUnknownSuggest
		}
		if s.Status != SuggestionPending {
			return nil, ErrBadStatus
		}
		if editedText != "" {
			s.Text = editedText
		}
		c.sendLocked(s.StudentID, s.Text, true)
		s.Status = SuggestionSent
		return []string{s.StudentID}, nil
	})
}

// DismissSuggestion drops a pending suggestion.
func (c *Classroom) DismissSuggestion(id string) error {
	return c.mutate(func() ([]string, error) {
		s := c.suggestionLocked(id)
		if s == nil {
			return nil, ErrUnknownSuggest
		}
		if s.Status != SuggestionPending {
			return nil, ErrBadStatus
		}
		s.Status = SuggestionDismissed
		return nil, nil
	})
}

func (c *Classroom) suggestionLocked(id string) *Suggestion {
	for _, s := range c.suggestions {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// Suggestions returns every suggestion, oldest first.
func (c *Classroom) Suggestions() []Suggestion {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Suggestion, 0, len(c.suggestions))
	for _, s := range c.suggestions {
		out = append(out, *s)
	}
	return out
}

// MessagesFor returns the messages received by a student.
func (c *Classroom) MessagesFor(sid string) []Message {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.messagesForLocked(sid)
}

func (c *Classroom) messagesForLocked(sid string) []Message {
	var out []Message
	for _, m := range c.messages {
		if m.StudentID == sid {
			out = append(out, m)
		}
	}
	return out
}

// Highlight raises an alert on a student card (used by Claude).
func (c *Classroom) Highlight(sid, qid, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ErrEmpty
	}
	if err := validText(reason, MaxMessageLen); err != nil {
		return err
	}
	return c.mutate(func() ([]string, error) {
		if _, ok := c.students[sid]; !ok {
			return nil, ErrUnknownStudent
		}
		if qid != "" {
			if _, ok := c.byQID[qid]; !ok {
				return nil, ErrUnknownQuestion
			}
		}
		c.addAlertLocked(sid, qid, "ai", reason, "claude", true)
		return nil, nil
	})
}

// addAlertLocked records an alert. Rule alerts are deduplicated per
// student/question/kind unless repeat is true.
func (c *Classroom) addAlertLocked(sid, qid, kind, msg, source string, repeat bool) {
	key := sid + "|" + qid + "|" + kind
	if !repeat && c.alertKeys[key] {
		return
	}
	c.alertKeys[key] = true
	a := Alert{ID: c.newIDLocked("a"), Seq: c.nextSeqLocked(), Time: c.now(), StudentID: sid, QuestionID: qid, Kind: kind, Message: msg, Source: source}
	c.alerts = append(c.alerts, a)
	if len(c.alerts) > maxAlerts {
		c.alerts = append([]Alert(nil), c.alerts[len(c.alerts)-maxAlerts:]...)
	}
}

// Alerts returns alerts, oldest first.
func (c *Classroom) Alerts() []Alert {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Alert(nil), c.alerts...)
}

// Thresholds used by the live rules.
type Thresholds struct {
	Inactive time.Duration // no input at all since the question opened
	Stalled  time.Duration // started typing, then nothing
	Hidden   time.Duration // page hidden for that long
}

// DefaultThresholds are tuned for a live class.
var DefaultThresholds = Thresholds{Inactive: 60 * time.Second, Stalled: 90 * time.Second, Hidden: 15 * time.Second}

// ApplyRules raises alerts for the open question. Called periodically.
func (c *Classroom) ApplyRules(t Thresholds) {
	c.mu.Lock()
	before := len(c.alerts)
	q := c.currentLocked()
	if q != nil {
		c.applyRulesLocked(q, t)
	}
	changed := len(c.alerts) != before
	if changed {
		c.rev++
	}
	n := c.notifier
	c.mu.Unlock()
	if changed {
		n.TeacherDirty()
	}
}

func (c *Classroom) applyRulesLocked(q *Question, t Thresholds) {
	now := c.now()
	for _, sid := range c.studentList {
		st := c.students[sid]
		if !st.Connected {
			continue
		}
		since := q.OpenedAt
		if st.connectedAt.After(since) {
			since = st.connectedAt
		}
		a := c.answers[q.ID][sid]
		if a != nil && a.Submitted {
			continue
		}
		switch {
		case (a == nil || a.FirstInputAt.IsZero()) && now.Sub(since) >= t.Inactive:
			c.addAlertLocked(sid, q.ID, "inactive", "Aucune saisie depuis l'ouverture de la question", "rules", false)
		case a != nil && !a.FirstInputAt.IsZero() && now.Sub(a.UpdatedAt) >= t.Stalled:
			c.addAlertLocked(sid, q.ID, "stalled", "Ne tape plus rien, peut-être bloqué", "rules", false)
		}
		if a != nil && a.Hidden && now.Sub(a.HiddenSince) >= t.Hidden {
			c.addAlertLocked(sid, q.ID, "hidden", "A quitté la page depuis plus de "+t.Hidden.String(), "rules", false)
		}
	}
}

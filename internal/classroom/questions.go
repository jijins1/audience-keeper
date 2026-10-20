package classroom

import (
	"cmp"
	"fmt"
	"strings"
)

var yesNoChoices = []string{"Oui", "Non"}

// NewQuestion describes a question to create.
type NewQuestion struct {
	Title      string
	Type       QuestionType
	NumChoices int    // for TypeChoice; defaults to 4
	ProposedBy string // "teacher" or "claude"
	Open       bool   // open immediately (teacher only)
	// Key makes creation idempotent: a question with the same key is reused
	// (and opened if still proposed) instead of duplicated. Used by slides.
	Key string
	// Code describes a TypeCode question; ignored otherwise.
	Code *CodeInput
}

// CreateQuestion adds a question, optionally opening it right away.
func (c *Classroom) CreateQuestion(nq NewQuestion) (Question, error) {
	title := strings.TrimSpace(nq.Title)
	if err := validText(title, MaxTitleLen); err != nil {
		return Question{}, err
	}
	choices, err := choicesFor(nq.Type, nq.NumChoices)
	if err != nil {
		return Question{}, err
	}
	var code *CodeSpec
	if nq.Type == TypeCode {
		if code, err = newCodeSpec(nq.Code); err != nil {
			return Question{}, err
		}
	}
	by := nq.ProposedBy
	if by == "" {
		by = "teacher"
	}
	key := strings.TrimSpace(nq.Key)
	if err := validText(key, MaxKeyLen); err != nil {
		return Question{}, err
	}
	var out Question
	err = c.mutate(func() ([]string, error) {
		if key != "" {
			if q := c.byKeyLocked(key); q != nil {
				var refresh []string
				if q.Status == StatusProposed {
					// the slide may have been edited since: it describes the question
					q.Title, q.Type, q.Choices, q.Code = cmp.Or(title, q.Title), nq.Type, choices, code
				}
				if nq.Open && q.Status == StatusProposed {
					c.openLocked(q)
					refresh = everyone()
				}
				out = copyQuestion(q)
				return refresh, nil
			}
		}
		if by != "teacher" && c.proposedLocked() >= MaxPending {
			return nil, ErrTooManyPending
		}
		c.nextQ++
		if title == "" {
			title = fmt.Sprintf("Question %d", len(c.questions)+1)
		}
		q := &Question{
			ID:         fmt.Sprintf("q%d", c.nextQ),
			Key:        key,
			Title:      title,
			Type:       nq.Type,
			Choices:    choices,
			Code:       code,
			Status:     StatusProposed,
			ProposedBy: by,
			CreatedAt:  c.now(),
		}
		c.questions = append(c.questions, q)
		c.byQID[q.ID] = q
		c.answers[q.ID] = map[string]*Answer{}
		var refresh []string
		if nq.Open {
			c.openLocked(q)
			refresh = everyone()
		}
		out = copyQuestion(q)
		return refresh, nil
	})
	return out, err
}

func (c *Classroom) byKeyLocked(key string) *Question {
	for _, q := range c.questions {
		if q.Key == key {
			return q
		}
	}
	return nil
}

func (c *Classroom) proposedLocked() int {
	n := 0
	for _, q := range c.questions {
		if q.Status == StatusProposed {
			n++
		}
	}
	return n
}

func choicesFor(t QuestionType, n int) ([]string, error) {
	switch t {
	case TypeText, TypeNumber, TypeCode:
		return nil, nil
	case TypeYesNo:
		return append([]string(nil), yesNoChoices...), nil
	case TypeChoice:
		if n == 0 {
			n = 4
		}
		if n < MinChoices || n > MaxChoices {
			return nil, ErrBadChoices
		}
		out := make([]string, n)
		for i := range out {
			out[i] = string(rune('A' + i))
		}
		return out, nil
	default:
		return nil, ErrBadType
	}
}

// OpenQuestion shows a proposed question to students. Any other open
// question is validated (closed) first.
func (c *Classroom) OpenQuestion(id string) error {
	return c.mutate(func() ([]string, error) {
		q, ok := c.byQID[id]
		if !ok {
			return nil, ErrUnknownQuestion
		}
		if q.Status != StatusProposed {
			return nil, ErrBadStatus
		}
		c.openLocked(q)
		return everyone(), nil
	})
}

func (c *Classroom) openLocked(q *Question) {
	if cur := c.currentLocked(); cur != nil {
		c.closeLocked(cur)
	}
	q.Status = StatusOpen
	q.OpenedAt = c.now()
}

// CloseQuestion validates a question: non-empty drafts are auto-submitted and
// answers are frozen.
func (c *Classroom) CloseQuestion(id string) error {
	return c.mutate(func() ([]string, error) {
		q, ok := c.byQID[id]
		if !ok {
			return nil, ErrUnknownQuestion
		}
		if q.Status != StatusOpen {
			return nil, ErrBadStatus
		}
		c.closeLocked(q)
		return everyone(), nil
	})
}

func (c *Classroom) closeLocked(q *Question) {
	now := c.now()
	for _, a := range c.answers[q.ID] {
		if a.Submitted || strings.TrimSpace(a.Draft) == "" {
			continue
		}
		v, err := normalizeValue(q, a.Draft)
		if err != nil {
			continue
		}
		a.Value = v
		a.Submitted = true
		a.AutoSubmitted = true
		a.SubmittedAt = now
		a.Seq = c.nextSeqLocked()
	}
	q.Status = StatusClosed
	q.ClosedAt = now
}

// DeleteQuestion removes a question that was never opened (e.g. a rejected
// proposal from Claude).
func (c *Classroom) DeleteQuestion(id string) error {
	return c.mutate(func() ([]string, error) {
		q, ok := c.byQID[id]
		if !ok {
			return nil, ErrUnknownQuestion
		}
		if q.Status != StatusProposed {
			return nil, ErrBadStatus
		}
		kept := make([]*Question, 0, len(c.questions))
		for _, x := range c.questions {
			if x.ID != id {
				kept = append(kept, x)
			}
		}
		c.questions = kept
		delete(c.byQID, id)
		delete(c.answers, id)
		return nil, nil
	})
}

// SetCorrect marks the accepted answers of a question (can be done after the fact).
func (c *Classroom) SetCorrect(id string, values []string) error {
	return c.mutate(func() ([]string, error) {
		q, ok := c.byQID[id]
		if !ok {
			return nil, ErrUnknownQuestion
		}
		out := make([]string, 0, len(values))
		seen := map[string]bool{}
		for _, v := range values {
			nv, err := normalizeValue(q, v)
			if err != nil {
				return nil, err
			}
			if k := answerKey(*q, nv); !seen[k] {
				seen[k] = true
				out = append(out, nv)
			}
		}
		q.Correct = out
		return nil, nil
	})
}

// SetGroups replaces the answer clusters of a question.
func (c *Classroom) SetGroups(id string, groups []Group) error {
	return c.mutate(func() ([]string, error) {
		q, ok := c.byQID[id]
		if !ok {
			return nil, ErrUnknownQuestion
		}
		out := make([]Group, 0, len(groups))
		for _, g := range groups {
			label := strings.TrimSpace(g.Label)
			if label == "" {
				return nil, ErrEmpty
			}
			if err := validText(label, MaxTitleLen); err != nil {
				return nil, err
			}
			if err := validText(g.Comment, MaxMessageLen); err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(g.StudentIDs))
			for _, sid := range g.StudentIDs {
				if _, ok := c.students[sid]; !ok {
					return nil, fmt.Errorf("%w: %s", ErrUnknownStudent, sid)
				}
				ids = append(ids, sid)
			}
			out = append(out, Group{Label: label, Comment: strings.TrimSpace(g.Comment), StudentIDs: ids})
		}
		q.Groups = out
		return nil, nil
	})
}

// Questions returns every question in creation order.
func (c *Classroom) Questions() []Question {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Question, 0, len(c.questions))
	for _, q := range c.questions {
		out = append(out, copyQuestion(q))
	}
	return out
}

// Question returns one question.
func (c *Classroom) Question(id string) (Question, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	q, ok := c.byQID[id]
	if !ok {
		return Question{}, ErrUnknownQuestion
	}
	return copyQuestion(q), nil
}

// Current returns the open question, if any.
func (c *Classroom) Current() (Question, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	q := c.currentLocked()
	if q == nil {
		return Question{}, false
	}
	return copyQuestion(q), true
}

func (c *Classroom) currentLocked() *Question {
	for i := len(c.questions) - 1; i >= 0; i-- {
		if c.questions[i].Status == StatusOpen {
			return c.questions[i]
		}
	}
	return nil
}

func copyQuestion(q *Question) Question {
	out := *q
	out.Choices = append([]string(nil), q.Choices...)
	out.Code = copyCode(q.Code)
	out.Correct = append([]string(nil), q.Correct...)
	out.Groups = make([]Group, len(q.Groups))
	for i, g := range q.Groups {
		out.Groups[i] = Group{Label: g.Label, Comment: g.Comment, StudentIDs: append([]string(nil), g.StudentIDs...)}
	}
	return out
}

package classroom

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// BurstThreshold is the number of characters inserted at once, without a
// paste event, that is flagged as suspicious (drag & drop, autotype...).
const BurstThreshold = 30

// pasteGrace is how long after a paste event a large insertion is attributed to it.
const pasteGrace = 2 * time.Second

func (c *Classroom) answerLocked(qid, sid string) *Answer {
	m := c.answers[qid]
	a, ok := m[sid]
	if !ok {
		a = &Answer{QuestionID: qid, StudentID: sid}
		m[sid] = a
	}
	return a
}

// openFor checks that sid exists and qid is the open question.
func (c *Classroom) openForLocked(qid, sid string) (*Question, error) {
	s, ok := c.students[sid]
	if !ok {
		return nil, ErrUnknownStudent
	}
	s.LastSeen = c.now()
	q, ok := c.byQID[qid]
	if !ok {
		return nil, ErrUnknownQuestion
	}
	if q.Status != StatusOpen {
		return nil, ErrNotOpen
	}
	return q, nil
}

// UpdateDraft records what the student is currently typing.
func (c *Classroom) UpdateDraft(sid, qid, text string) error {
	if err := validText(text, MaxAnswerLen); err != nil {
		return err
	}
	return c.mutate(func() ([]string, error) {
		if _, err := c.openForLocked(qid, sid); err != nil {
			return nil, err
		}
		a := c.answerLocked(qid, sid)
		if a.Submitted {
			return nil, ErrAlreadyAnswered
		}
		now := c.now()
		delta := utf8.RuneCountInString(text) - utf8.RuneCountInString(a.Draft)
		if delta >= BurstThreshold && now.Sub(a.lastPasteAt) > pasteGrace {
			a.Bursts++
			c.addEventLocked(Event{StudentID: sid, QuestionID: qid, Kind: EventBurst, Chars: delta})
			c.addAlertLocked(sid, qid, "burst", "Ajout soudain de "+strconv.Itoa(delta)+" caractères sans frappe", "rules", false)
		}
		if a.FirstInputAt.IsZero() && text != "" {
			a.FirstInputAt = now
		}
		a.Draft = text
		a.UpdatedAt = now
		a.Seq = c.nextSeqLocked()
		return nil, nil
	})
}

// ClientEvent is a behavioural signal reported by the student page.
type ClientEvent struct {
	Kind       EventKind
	Chars      int
	DurationMs int64
	Text       string
}

// RecordEvent stores a signal sent by the browser (paste, copy, cut, blur, focus).
func (c *Classroom) RecordEvent(sid, qid string, ev ClientEvent) error {
	switch ev.Kind {
	case EventPaste, EventCopy, EventCut, EventBlur, EventFocus:
	default:
		return ErrBadEvent
	}
	if ev.Chars < 0 || ev.Chars > MaxAnswerLen*4 || ev.DurationMs < 0 || ev.DurationMs > int64(24*time.Hour/time.Millisecond) {
		return ErrBadEvent
	}
	ev.Text = truncateRunes(ev.Text, MaxPasteExcerpt)
	return c.mutate(func() ([]string, error) {
		if _, err := c.openForLocked(qid, sid); err != nil {
			return nil, err
		}
		a := c.answerLocked(qid, sid)
		now := c.now()
		e := Event{StudentID: sid, QuestionID: qid, Kind: ev.Kind, Chars: ev.Chars}
		switch ev.Kind {
		case EventPaste:
			a.Pastes++
			a.PastedChars += ev.Chars
			a.lastPasteAt = now
			e.Text = ev.Text
			c.addAlertLocked(sid, qid, "paste", "A collé "+strconv.Itoa(ev.Chars)+" caractères", "rules", false)
		case EventCopy, EventCut:
			a.Copies++
		case EventBlur:
			if !a.Hidden {
				a.Hidden = true
				a.HiddenSince = now
				a.Blurs++
			}
		case EventFocus:
			if a.Hidden {
				d := now.Sub(a.HiddenSince).Milliseconds()
				a.BlurMs += d
				e.DurationMs = d
				a.Hidden = false
				a.HiddenSince = time.Time{}
			}
		}
		c.addEventLocked(e)
		a.Seq = c.nextSeqLocked()
		return nil, nil
	})
}

// Submit records the final answer of a student. One answer per question.
func (c *Classroom) Submit(sid, qid, value string) error {
	if err := validText(value, MaxAnswerLen); err != nil {
		return err
	}
	return c.mutate(func() ([]string, error) {
		q, err := c.openForLocked(qid, sid)
		if err != nil {
			return nil, err
		}
		a := c.answerLocked(qid, sid)
		if a.Submitted {
			return nil, ErrAlreadyAnswered
		}
		v, err := normalizeValue(q, value)
		if err != nil {
			return nil, err
		}
		now := c.now()
		if a.FirstInputAt.IsZero() {
			a.FirstInputAt = now
		}
		a.Value = v
		if q.Type == TypeText || q.Type == TypeNumber {
			a.Draft = value
		}
		a.Submitted = true
		a.SubmittedAt = now
		a.UpdatedAt = now
		a.Seq = c.nextSeqLocked()
		c.addEventLocked(Event{StudentID: sid, QuestionID: qid, Kind: EventSubmit})
		return []string{sid}, nil
	})
}

// Grade sets a teacher or AI evaluation on an answer.
func (c *Classroom) Grade(qid, sid string, g Grade, byTeacher bool, note string) error {
	switch g {
	case GradeNone, GradeCorrect, GradePartial, GradeWrong:
	default:
		return ErrBadGrade
	}
	if err := validText(note, MaxMessageLen); err != nil {
		return err
	}
	return c.mutate(func() ([]string, error) {
		if _, ok := c.byQID[qid]; !ok {
			return nil, ErrUnknownQuestion
		}
		if _, ok := c.students[sid]; !ok {
			return nil, ErrUnknownStudent
		}
		a := c.answerLocked(qid, sid)
		if byTeacher {
			a.TeacherGrade = g
		} else {
			if g != GradeNone {
				a.AIGrade = g
			}
			if note != "" {
				a.AINote = strings.TrimSpace(note)
			}
		}
		a.Seq = c.nextSeqLocked()
		return nil, nil
	})
}

// Answers returns the answers of a question, in student join order.
func (c *Classroom) Answers(qid string) ([]Answer, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if _, ok := c.byQID[qid]; !ok {
		return nil, ErrUnknownQuestion
	}
	return c.answersLocked(qid), nil
}

func (c *Classroom) answersLocked(qid string) []Answer {
	m := c.answers[qid]
	out := make([]Answer, 0, len(m))
	for _, sid := range c.studentList {
		if a, ok := m[sid]; ok {
			out = append(out, *a)
		}
	}
	return out
}

// normalizeValue validates a submitted value against the question type.
func normalizeValue(q *Question, raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", ErrEmpty
	}
	switch q.Type {
	case TypeChoice, TypeYesNo:
		for _, ch := range q.Choices {
			if strings.EqualFold(ch, v) {
				return ch, nil
			}
		}
		return "", ErrBadAnswer
	case TypeNumber:
		f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > MaxNumber {
			return "", ErrBadAnswer
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	default:
		return v, nil
	}
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

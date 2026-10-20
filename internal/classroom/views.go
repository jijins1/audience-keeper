package classroom

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"time"
)

// PublicQuestion is what students see.
type PublicQuestion struct {
	ID      string       `json:"id"`
	Title   string       `json:"title"`
	Type    QuestionType `json:"type"`
	Choices []string     `json:"choices,omitempty"`
}

// StudentView is pushed to a student page.
type StudentView struct {
	StudentID string          `json:"studentId"`
	Pseudo    string          `json:"pseudo"`
	Question  *PublicQuestion `json:"question"`
	Draft     string          `json:"draft"`
	Submitted bool            `json:"submitted"`
	Value     string          `json:"value,omitempty"`
	Messages  []Message       `json:"messages"`
	Slide     *LiveSlide      `json:"slide"`
}

// StudentView builds the state of a student page.
func (c *Classroom) StudentView(sid string) (StudentView, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.students[sid]
	if !ok {
		return StudentView{}, ErrUnknownStudent
	}
	v := StudentView{StudentID: sid, Pseudo: s.Pseudo, Messages: c.messagesForLocked(sid)}
	if v.Messages == nil {
		v.Messages = []Message{}
	}
	if c.slide != nil {
		s := copySlide(c.slide)
		v.Slide = &s
	}
	if q := c.currentLocked(); q != nil {
		v.Question = &PublicQuestion{ID: q.ID, Title: q.Title, Type: q.Type, Choices: append([]string(nil), q.Choices...)}
		if a, ok := c.answers[q.ID][sid]; ok {
			v.Draft, v.Submitted, v.Value = a.Draft, a.Submitted, a.Value
		}
	}
	return v, nil
}

// QuestionSummary is a line of the question list.
type QuestionSummary struct {
	Question
	Answered int `json:"answered"`
}

// QuestionDetail is a question with all its answers and live stats.
type QuestionDetail struct {
	Question Question `json:"question"`
	Answers  []Answer `json:"answers"`
	Stats    Stats    `json:"stats"`
}

// TeacherView is pushed to the teacher dashboard.
type TeacherView struct {
	Code        string            `json:"code"`
	StartedAt   time.Time         `json:"startedAt"`
	AIMode      AIMode            `json:"aiMode"`
	Students    []Student         `json:"students"`
	Questions   []QuestionSummary `json:"questions"`
	CurrentID   string            `json:"currentId"`
	View        *QuestionDetail   `json:"view"`
	Alerts      []Alert           `json:"alerts"`
	Suggestions []Suggestion      `json:"suggestions"`
	Seq         int64             `json:"seq"`
	Live        *LiveInfo         `json:"live"`
}

// LiveInfo tells the dashboard which slide students are seeing.
type LiveInfo struct {
	Deck string `json:"deck"`
	H    int    `json:"h"`
	V    int    `json:"v"`
}

const (
	teacherAlerts   = 80
	teacherDraftLen = 1000 // per card, keeps 4 Hz frames small; full text via export/MCP
)

// TeacherView builds the dashboard state, detailing viewQID (or the open
// question, or the latest one, when empty or unknown).
func (c *Classroom) TeacherView(viewQID string) TeacherView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v := TeacherView{Code: c.code, StartedAt: c.startedAt, AIMode: c.aiMode, Students: c.studentsLocked(), Seq: c.seq,
		Questions: make([]QuestionSummary, 0, len(c.questions))}
	for _, q := range c.questions {
		n := 0
		for _, a := range c.answers[q.ID] {
			if a.Submitted {
				n++
			}
		}
		v.Questions = append(v.Questions, QuestionSummary{Question: copyQuestion(q), Answered: n})
	}
	cur := c.currentLocked()
	if cur != nil {
		v.CurrentID = cur.ID
	}
	if c.slide != nil {
		v.Live = &LiveInfo{Deck: c.slide.Deck, H: c.slide.H, V: c.slide.V}
	}
	q, ok := c.byQID[viewQID]
	if !ok {
		q = cur
	}
	if q == nil && len(c.questions) > 0 {
		q = c.questions[len(c.questions)-1]
	}
	if q != nil {
		answers := c.answersLocked(q.ID)
		stats := computeStats(*q, answers, len(c.studentList))
		for i := range answers {
			answers[i].Draft = truncateRunes(answers[i].Draft, teacherDraftLen)
			answers[i].Value = truncateRunes(answers[i].Value, teacherDraftLen)
		}
		v.View = &QuestionDetail{Question: copyQuestion(q), Answers: answers, Stats: stats}
	}
	start := max(0, len(c.alerts)-teacherAlerts)
	v.Alerts = append([]Alert{}, c.alerts[start:]...)
	v.Suggestions = []Suggestion{}
	for _, s := range c.suggestions {
		if s.Status == SuggestionPending {
			v.Suggestions = append(v.Suggestions, *s)
		}
	}
	return v
}

// Changes lists what happened after a cursor (sequence number).
type Changes struct {
	Cursor  int64    `json:"cursor"`
	Events  []Event  `json:"events"`
	Answers []Answer `json:"answers"`
	Alerts  []Alert  `json:"alerts"`
	More    bool     `json:"more"`
}

const maxChanges = 500

// ChangesSince returns events, updated answers and alerts after cursor.
func (c *Classroom) ChangesSince(cursor int64) Changes {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := Changes{Cursor: c.seq, Events: []Event{}, Answers: []Answer{}, Alerts: []Alert{}}
	for _, e := range c.events {
		if e.Seq <= cursor {
			continue
		}
		if len(out.Events) == maxChanges {
			// oldest first: the caller resumes from the last returned event
			out.More = true
			out.Cursor = out.Events[len(out.Events)-1].Seq
			break
		}
		out.Events = append(out.Events, e)
	}
	for _, q := range c.questions {
		for _, a := range c.answersLocked(q.ID) {
			if a.Seq > cursor {
				out.Answers = append(out.Answers, a)
			}
		}
	}
	for _, a := range c.alerts {
		if a.Seq > cursor {
			out.Alerts = append(out.Alerts, a)
		}
	}
	return out
}

// HistoryItem is one answer of a student with its question.
type HistoryItem struct {
	QuestionID    string         `json:"questionId"`
	QuestionTitle string         `json:"questionTitle"`
	Status        QuestionStatus `json:"status"`
	Answer        *Answer        `json:"answer"`
	Outcome       Grade          `json:"outcome,omitempty"`
}

// StudentHistory returns every question with the student's answer.
func (c *Classroom) StudentHistory(sid string) (Student, []HistoryItem, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.students[sid]
	if !ok {
		return Student{}, nil, ErrUnknownStudent
	}
	items := []HistoryItem{}
	for _, q := range c.questions {
		if q.Status == StatusProposed {
			continue
		}
		it := HistoryItem{QuestionID: q.ID, QuestionTitle: q.Title, Status: q.Status}
		if a, ok := c.answers[q.ID][sid]; ok {
			cp := *a
			it.Answer = &cp
			it.Outcome = IsCorrect(*q, cp)
		}
		items = append(items, it)
	}
	return *s, items, nil
}

// StudentSummary aggregates a student's behaviour over the session.
type StudentSummary struct {
	Student
	Answered int   `json:"answered"`
	Correct  int   `json:"correct"`
	Partial  int   `json:"partial"`
	Wrong    int   `json:"wrong"`
	Pastes   int   `json:"pastes"`
	Bursts   int   `json:"bursts"`
	Blurs    int   `json:"blurs"`
	BlurMs   int64 `json:"blurMs"`
	Alerts   int   `json:"alerts"`
}

// Summary returns per-student aggregates.
func (c *Classroom) Summary() []StudentSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]StudentSummary, 0, len(c.studentList))
	for _, sid := range c.studentList {
		ss := StudentSummary{Student: *c.students[sid]}
		for _, q := range c.questions {
			a, ok := c.answers[q.ID][sid]
			if !ok {
				continue
			}
			if a.Submitted {
				ss.Answered++
			}
			switch IsCorrect(*q, *a) {
			case GradeCorrect:
				ss.Correct++
			case GradePartial:
				ss.Partial++
			case GradeWrong:
				ss.Wrong++
			}
			ss.Pastes += a.Pastes
			ss.Bursts += a.Bursts
			ss.Blurs += a.Blurs
			ss.BlurMs += a.BlurMs
		}
		for _, al := range c.alerts {
			if al.StudentID == sid {
				ss.Alerts++
			}
		}
		out = append(out, ss)
	}
	return out
}

// ExportCSV writes every answer of the session as CSV. Rows are built under
// the lock and written after it is released, so a slow download never stalls
// the classroom.
func (c *Classroom) ExportCSV(w io.Writer) error {
	rows := c.csvRows()
	cw := csv.NewWriter(w)
	if err := cw.WriteAll(rows); err != nil {
		return fmt.Errorf("write csv: %w", err)
	}
	return nil
}

func (c *Classroom) csvRows() [][]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rows := [][]string{{"question_id", "question", "type", "student_id", "pseudo", "answer", "submitted", "auto_submitted",
		"response_ms", "pastes", "pasted_chars", "bursts", "blurs", "blur_ms", "outcome", "ai_grade", "ai_note"}}
	for _, q := range c.questions {
		for _, a := range c.answersLocked(q.ID) {
			resp := ""
			if a.Submitted && !a.AutoSubmitted && !q.OpenedAt.IsZero() {
				resp = strconv.FormatInt(a.SubmittedAt.Sub(q.OpenedAt).Milliseconds(), 10)
			}
			val := a.Value
			if !a.Submitted {
				val = a.Draft
			}
			if q.Type != TypeNumber || !a.Submitted { // normalised numbers are safe and must stay numeric
				val = csvSafe(val)
			}
			rows = append(rows, []string{q.ID, csvSafe(q.Title), string(q.Type), a.StudentID, csvSafe(c.students[a.StudentID].Pseudo), val,
				strconv.FormatBool(a.Submitted), strconv.FormatBool(a.AutoSubmitted), resp,
				strconv.Itoa(a.Pastes), strconv.Itoa(a.PastedChars), strconv.Itoa(a.Bursts), strconv.Itoa(a.Blurs),
				strconv.FormatInt(a.BlurMs, 10), string(IsCorrect(*q, a)), string(a.AIGrade), csvSafe(a.AINote)})
		}
	}
	return rows
}

// csvSafe neutralises spreadsheet formula injection.
func csvSafe(s string) string {
	if s != "" && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@' || s[0] == '\t' || s[0] == '\r') {
		return "'" + s
	}
	return s
}

package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"audience-keeper/internal/classroom"
)

// ---- output shapes (timestamps as RFC 3339 strings, snake_case keys) ----

type studentOut struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Connected bool   `json:"connected"`
	Answered  int    `json:"answered"`
	Correct   int    `json:"correct"`
	Partial   int    `json:"partial"`
	Wrong     int    `json:"wrong"`
	Pastes    int    `json:"pastes"`
	Bursts    int    `json:"bursts"`
	Blurs     int    `json:"blurs"`
	BlurMs    int64  `json:"blur_ms"`
	Alerts    int    `json:"alerts"`
}

type groupOut struct {
	Label      string   `json:"label"`
	Comment    string   `json:"comment,omitempty"`
	StudentIDs []string `json:"student_ids"`
}

type questionOut struct {
	ID         string     `json:"id"`
	Key        string     `json:"key,omitempty" jsonschema:"slide reveal.js d'origine, si la question vient d'un diaporama"`
	Title      string     `json:"title"`
	Type       string     `json:"type"`
	Choices    []string   `json:"choices,omitempty"`
	Status     string     `json:"status"`
	ProposedBy string     `json:"proposed_by"`
	OpenedAt   string     `json:"opened_at,omitempty"`
	ClosedAt   string     `json:"closed_at,omitempty"`
	Correct    []string   `json:"correct,omitempty"`
	Groups     []groupOut `json:"groups,omitempty"`
}

type answerOut struct {
	StudentID     string `json:"student_id"`
	Name          string `json:"name,omitempty"`
	Value         string `json:"value,omitempty"`
	Draft         string `json:"draft,omitempty"`
	Submitted     bool   `json:"submitted"`
	AutoSubmitted bool   `json:"auto_submitted,omitempty"`
	ResponseMs    int64  `json:"response_ms,omitempty"`
	Pastes        int    `json:"pastes,omitempty"`
	PastedChars   int    `json:"pasted_chars,omitempty"`
	Copies        int    `json:"copies,omitempty"`
	Bursts        int    `json:"bursts,omitempty"`
	Blurs         int    `json:"blurs,omitempty"`
	BlurMs        int64  `json:"blur_ms,omitempty"`
	HiddenNow     bool   `json:"hidden_now,omitempty"`
	Outcome       string `json:"outcome,omitempty"`
	TeacherGrade  string `json:"teacher_grade,omitempty"`
	AIGrade       string `json:"ai_grade,omitempty"`
	AINote        string `json:"ai_note,omitempty"`
	Seq           int64  `json:"seq"`
}

type eventOut struct {
	Seq        int64  `json:"seq"`
	Time       string `json:"time"`
	StudentID  string `json:"student_id"`
	QuestionID string `json:"question_id,omitempty"`
	Kind       string `json:"kind"`
	Chars      int    `json:"chars,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Text       string `json:"text,omitempty"`
}

type alertOut struct {
	Seq        int64  `json:"seq"`
	Time       string `json:"time"`
	StudentID  string `json:"student_id"`
	QuestionID string `json:"question_id,omitempty"`
	Kind       string `json:"kind"`
	Message    string `json:"message"`
	Source     string `json:"source"`
}

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (t *tools) names() map[string]string {
	if !t.opts.ShowNames {
		return nil
	}
	m := map[string]string{}
	for _, s := range t.room.Students() {
		m[s.ID] = s.Pseudo
	}
	return m
}

func toQuestion(q classroom.Question) questionOut {
	out := questionOut{ID: q.ID, Key: q.Key, Title: q.Title, Type: string(q.Type), Choices: q.Choices, Status: string(q.Status),
		ProposedBy: q.ProposedBy, OpenedAt: ts(q.OpenedAt), ClosedAt: ts(q.ClosedAt), Correct: q.Correct}
	for _, g := range q.Groups {
		out.Groups = append(out.Groups, groupOut{Label: g.Label, Comment: g.Comment, StudentIDs: g.StudentIDs})
	}
	return out
}

func toAnswer(q classroom.Question, a classroom.Answer, names map[string]string) answerOut {
	out := answerOut{StudentID: a.StudentID, Name: names[a.StudentID], Value: a.Value, Submitted: a.Submitted,
		AutoSubmitted: a.AutoSubmitted, Pastes: a.Pastes, PastedChars: a.PastedChars, Copies: a.Copies, Bursts: a.Bursts,
		Blurs: a.Blurs, BlurMs: a.BlurMs, HiddenNow: a.Hidden, Outcome: string(classroom.IsCorrect(q, a)),
		TeacherGrade: string(a.TeacherGrade), AIGrade: string(a.AIGrade), AINote: a.AINote, Seq: a.Seq}
	if !a.Submitted {
		out.Draft = a.Draft
	}
	if a.Submitted && !q.OpenedAt.IsZero() {
		out.ResponseMs = a.SubmittedAt.Sub(q.OpenedAt).Milliseconds()
	}
	return out
}

// ---- handlers ----

type empty struct{}

type summaryOut struct {
	StartedAt         string        `json:"started_at"`
	AIMode            string        `json:"ai_mode"`
	CurrentQuestionID string        `json:"current_question_id,omitempty"`
	Students          []studentOut  `json:"students"`
	Questions         []questionOut `json:"questions"`
}

func (t *tools) sessionSummary(_ context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, summaryOut, error) {
	names := t.names()
	out := summaryOut{StartedAt: ts(t.room.StartedAt()), AIMode: string(t.room.AIMode()), Students: []studentOut{}, Questions: []questionOut{}}
	if q, ok := t.room.Current(); ok {
		out.CurrentQuestionID = q.ID
	}
	for _, s := range t.room.Summary() {
		out.Students = append(out.Students, studentOut{ID: s.ID, Name: names[s.ID], Connected: s.Connected, Answered: s.Answered,
			Correct: s.Correct, Partial: s.Partial, Wrong: s.Wrong, Pastes: s.Pastes, Bursts: s.Bursts, Blurs: s.Blurs, BlurMs: s.BlurMs, Alerts: s.Alerts})
	}
	for _, q := range t.room.Questions() {
		out.Questions = append(out.Questions, toQuestion(q))
	}
	return nil, out, nil
}

type questionIn struct {
	QuestionID string `json:"question_id" jsonschema:"identifiant de la question, ex. q3"`
}

type answersOut struct {
	Question questionOut `json:"question"`
	Answers  []answerOut `json:"answers"`
}

func (t *tools) answers(_ context.Context, _ *mcp.CallToolRequest, in questionIn) (*mcp.CallToolResult, answersOut, error) {
	q, err := t.room.Question(in.QuestionID)
	if err != nil {
		return nil, answersOut{}, err
	}
	as, err := t.room.Answers(in.QuestionID)
	if err != nil {
		return nil, answersOut{}, err
	}
	names := t.names()
	out := answersOut{Question: toQuestion(q), Answers: []answerOut{}}
	for _, a := range as {
		out.Answers = append(out.Answers, toAnswer(q, a, names))
	}
	return nil, out, nil
}

func (t *tools) stats(_ context.Context, _ *mcp.CallToolRequest, in questionIn) (*mcp.CallToolResult, classroom.Stats, error) {
	s, err := t.room.Stats(in.QuestionID)
	return nil, s, err
}

type changesIn struct {
	Cursor int64 `json:"cursor" jsonschema:"cursor renvoyé par l'appel précédent, 0 la première fois"`
}

type changesOut struct {
	Cursor  int64       `json:"cursor"`
	Events  []eventOut  `json:"events"`
	Answers []answerOut `json:"answers"`
	Alerts  []alertOut  `json:"alerts"`
	More    bool        `json:"more"`
}

func (t *tools) changes(_ context.Context, _ *mcp.CallToolRequest, in changesIn) (*mcp.CallToolResult, changesOut, error) {
	ch := t.room.ChangesSince(in.Cursor)
	names := t.names()
	qs := map[string]classroom.Question{}
	for _, q := range t.room.Questions() {
		qs[q.ID] = q
	}
	out := changesOut{Cursor: ch.Cursor, More: ch.More, Events: []eventOut{}, Answers: []answerOut{}, Alerts: []alertOut{}}
	for _, e := range ch.Events {
		out.Events = append(out.Events, eventOut{Seq: e.Seq, Time: ts(e.Time), StudentID: e.StudentID, QuestionID: e.QuestionID,
			Kind: string(e.Kind), Chars: e.Chars, DurationMs: e.DurationMs, Text: e.Text})
	}
	for _, a := range ch.Answers {
		out.Answers = append(out.Answers, toAnswer(qs[a.QuestionID], a, names))
	}
	for _, a := range ch.Alerts {
		out.Alerts = append(out.Alerts, alertOut{Seq: a.Seq, Time: ts(a.Time), StudentID: a.StudentID, QuestionID: a.QuestionID,
			Kind: a.Kind, Message: a.Message, Source: a.Source})
	}
	return nil, out, nil
}

type studentIn struct {
	StudentID string `json:"student_id" jsonschema:"identifiant de l'étudiant, ex. s4"`
}

type historyItemOut struct {
	QuestionID    string     `json:"question_id"`
	QuestionTitle string     `json:"question_title"`
	Status        string     `json:"status"`
	Answer        *answerOut `json:"answer,omitempty"`
}

type historyOut struct {
	StudentID string           `json:"student_id"`
	Name      string           `json:"name,omitempty"`
	Connected bool             `json:"connected"`
	Items     []historyItemOut `json:"items"`
}

func (t *tools) history(_ context.Context, _ *mcp.CallToolRequest, in studentIn) (*mcp.CallToolResult, historyOut, error) {
	st, items, err := t.room.StudentHistory(in.StudentID)
	if err != nil {
		return nil, historyOut{}, err
	}
	names := t.names()
	out := historyOut{StudentID: st.ID, Name: names[st.ID], Connected: st.Connected, Items: []historyItemOut{}}
	for _, it := range items {
		h := historyItemOut{QuestionID: it.QuestionID, QuestionTitle: it.QuestionTitle, Status: string(it.Status)}
		if it.Answer != nil {
			q, _ := t.room.Question(it.QuestionID)
			a := toAnswer(q, *it.Answer, names)
			h.Answer = &a
		}
		out.Items = append(out.Items, h)
	}
	return nil, out, nil
}

type proposeIn struct {
	Title   string `json:"title" jsonschema:"énoncé de la question, 300 caractères max"`
	Type    string `json:"type" jsonschema:"text, choice, yesno ou number"`
	Choices int    `json:"choices,omitempty" jsonschema:"nombre de choix pour le type choice (2 à 8, défaut 4)"`
}

func (t *tools) proposeQuestion(_ context.Context, _ *mcp.CallToolRequest, in proposeIn) (*mcp.CallToolResult, questionOut, error) {
	q, err := t.room.CreateQuestion(classroom.NewQuestion{Title: in.Title, Type: classroom.QuestionType(in.Type), NumChoices: in.Choices, ProposedBy: "claude"})
	if err != nil {
		return nil, questionOut{}, err
	}
	return nil, toQuestion(q), nil
}

type groupsIn struct {
	QuestionID string     `json:"question_id" jsonschema:"identifiant de la question"`
	Groups     []groupOut `json:"groups" jsonschema:"groupes : label court, commentaire optionnel, student_ids"`
}

type okOut struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func (t *tools) setGroups(_ context.Context, _ *mcp.CallToolRequest, in groupsIn) (*mcp.CallToolResult, okOut, error) {
	gs := make([]classroom.Group, 0, len(in.Groups))
	for _, g := range in.Groups {
		gs = append(gs, classroom.Group{Label: g.Label, Comment: g.Comment, StudentIDs: g.StudentIDs})
	}
	if err := t.room.SetGroups(in.QuestionID, gs); err != nil {
		return nil, okOut{}, err
	}
	return nil, okOut{OK: true}, nil
}

type annotation struct {
	StudentID string `json:"student_id"`
	Grade     string `json:"grade,omitempty" jsonschema:"correct, partial, wrong, ou vide pour ne pas noter"`
	Note      string `json:"note,omitempty" jsonschema:"remarque courte pour l'enseignant"`
}

type annotateIn struct {
	QuestionID  string       `json:"question_id"`
	Annotations []annotation `json:"annotations"`
}

func (t *tools) annotate(_ context.Context, _ *mcp.CallToolRequest, in annotateIn) (*mcp.CallToolResult, okOut, error) {
	for _, a := range in.Annotations {
		switch classroom.Grade(a.Grade) {
		case classroom.GradeNone, classroom.GradeCorrect, classroom.GradePartial, classroom.GradeWrong:
		default:
			return nil, okOut{}, fmt.Errorf("%w: %s (aucune annotation appliquée)", classroom.ErrBadGrade, a.StudentID)
		}
	}
	for _, a := range in.Annotations {
		if err := t.room.Grade(in.QuestionID, a.StudentID, classroom.Grade(a.Grade), false, a.Note); err != nil {
			return nil, okOut{}, err
		}
	}
	return nil, okOut{OK: true}, nil
}

type highlightIn struct {
	StudentID  string `json:"student_id"`
	QuestionID string `json:"question_id,omitempty" jsonschema:"question concernée, optionnelle"`
	Reason     string `json:"reason" jsonschema:"raison courte affichée à l'enseignant"`
}

func (t *tools) highlight(_ context.Context, _ *mcp.CallToolRequest, in highlightIn) (*mcp.CallToolResult, okOut, error) {
	if err := t.room.Highlight(in.StudentID, in.QuestionID, in.Reason); err != nil {
		return nil, okOut{}, err
	}
	return nil, okOut{OK: true}, nil
}

type messageIn struct {
	StudentID string `json:"student_id"`
	Message   string `json:"message" jsonschema:"message court et bienveillant, 500 caractères max"`
}

type messageOut struct {
	SuggestionID string `json:"suggestion_id"`
	Status       string `json:"status" jsonschema:"pending (attend l'enseignant) ou sent"`
}

func (t *tools) message(_ context.Context, _ *mcp.CallToolRequest, in messageIn) (*mcp.CallToolResult, messageOut, error) {
	s, err := t.room.SuggestMessage(in.StudentID, in.Message)
	if err != nil {
		return nil, messageOut{}, err
	}
	return nil, messageOut{SuggestionID: s.ID, Status: string(s.Status)}, nil
}

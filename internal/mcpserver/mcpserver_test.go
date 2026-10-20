package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"audience-keeper/internal/classroom"
)

type fixture struct {
	room    *classroom.Classroom
	session *mcp.ClientSession
	lea     string
	tom     string
	qid     string
}

func setup(t *testing.T, opts Options) *fixture {
	t.Helper()
	room := classroom.New(classroom.WithCode("ABC123"))
	lea, _, _ := room.Join("ABC123", "Léa")
	tom, _, _ := room.Join("ABC123", "Tom")
	_ = room.Connect(lea.ID)
	q, _ := room.CreateQuestion(classroom.NewQuestion{Title: "Goroutine ?", Type: classroom.TypeText, Open: true})
	_ = room.RecordEvent(lea.ID, q.ID, classroom.ClientEvent{Kind: classroom.EventPaste, Chars: 20, Text: "texte collé"})
	_ = room.Submit(lea.ID, q.ID, "un thread léger")
	_ = room.UpdateDraft(tom.ID, q.ID, "je sais p")

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	server := New(room, opts)
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return &fixture{room: room, session: cs, lea: lea.ID, tom: tom.ID, qid: q.ID}
}

// call invokes a tool and decodes its structured output into out.
func (f *fixture) call(t *testing.T, name string, args any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := f.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s decode: %v", name, err)
		}
	}
	return res
}

func errText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestListTools(t *testing.T) {
	f := setup(t, Options{})
	res, err := f.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"get_session_summary": true, "get_answers": true, "get_question_stats": true, "get_changes_since": true,
		"get_student_history": true, "propose_question": true, "set_groups": true, "annotate_answers": true,
		"highlight_student": true, "message_student": true}
	for _, tool := range res.Tools {
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing tools: %v", want)
	}
}

func TestReadTools(t *testing.T) {
	f := setup(t, Options{})

	var sum summaryOut
	f.call(t, "get_session_summary", map[string]any{}, &sum)
	if sum.CurrentQuestionID != f.qid || len(sum.Students) != 2 || sum.Students[0].Pastes != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	if sum.Students[0].Name != "" {
		t.Fatal("names must be hidden by default")
	}

	var ans answersOut
	f.call(t, "get_answers", map[string]any{"question_id": f.qid}, &ans)
	if len(ans.Answers) != 2 || ans.Answers[0].Value != "un thread léger" || ans.Answers[1].Draft != "je sais p" || ans.Answers[0].PastedChars != 20 {
		t.Fatalf("answers: %+v", ans)
	}
	if res := f.call(t, "get_answers", map[string]any{"question_id": "q404"}, nil); !res.IsError {
		t.Fatal("unknown question must be a tool error")
	}

	var st classroom.Stats
	f.call(t, "get_question_stats", map[string]any{"question_id": f.qid}, &st)
	if st.Answered != 1 || st.Typing != 1 || st.WithPaste != 1 {
		t.Fatalf("stats: %+v", st)
	}

	var ch changesOut
	f.call(t, "get_changes_since", map[string]any{"cursor": 0}, &ch)
	if ch.Cursor == 0 || len(ch.Events) == 0 || len(ch.Answers) != 2 || len(ch.Alerts) != 1 {
		t.Fatalf("changes: %+v", ch)
	}
	var ch2 changesOut
	f.call(t, "get_changes_since", map[string]any{"cursor": ch.Cursor}, &ch2)
	if len(ch2.Events)+len(ch2.Answers)+len(ch2.Alerts) != 0 {
		t.Fatalf("second poll should be empty: %+v", ch2)
	}

	var hist historyOut
	f.call(t, "get_student_history", map[string]any{"student_id": f.lea}, &hist)
	if len(hist.Items) != 1 || hist.Items[0].Answer == nil || hist.Items[0].Answer.Value != "un thread léger" {
		t.Fatalf("history: %+v", hist)
	}
}

func TestShowNames(t *testing.T) {
	f := setup(t, Options{ShowNames: true})
	var sum summaryOut
	f.call(t, "get_session_summary", map[string]any{}, &sum)
	if sum.Students[0].Name != "Léa" {
		t.Fatalf("names should be visible: %+v", sum.Students[0])
	}
}

func TestWriteTools(t *testing.T) {
	f := setup(t, Options{})

	var q questionOut
	f.call(t, "propose_question", map[string]any{"title": "Canal bufferisé ?", "type": "yesno"}, &q)
	if q.Status != "proposed" || q.ProposedBy != "claude" {
		t.Fatalf("proposed question must wait for the teacher: %+v", q)
	}
	if cur, _ := f.room.Current(); cur.ID != f.qid {
		t.Fatal("proposing must not change the open question")
	}
	if res := f.call(t, "propose_question", map[string]any{"title": "x", "type": "essay"}, nil); !res.IsError {
		t.Fatal("bad type accepted")
	}

	f.call(t, "set_groups", map[string]any{"question_id": f.qid, "groups": []map[string]any{
		{"label": "Bonne intuition", "student_ids": []string{f.lea}},
		{"label": "Ne sait pas", "comment": "à relancer", "student_ids": []string{f.tom}},
	}}, nil)
	got, _ := f.room.Question(f.qid)
	if len(got.Groups) != 2 || got.Groups[1].Comment != "à relancer" {
		t.Fatalf("groups: %+v", got.Groups)
	}
	if res := f.call(t, "set_groups", map[string]any{"question_id": f.qid, "groups": []map[string]any{{"label": "x", "student_ids": []string{"s999"}}}}, nil); !res.IsError {
		t.Fatal("unknown student accepted")
	}

	f.call(t, "annotate_answers", map[string]any{"question_id": f.qid, "annotations": []map[string]any{
		{"student_id": f.lea, "grade": "partial", "note": "idée juste, vocabulaire flou"},
	}}, nil)
	as, _ := f.room.Answers(f.qid)
	if as[0].AIGrade != classroom.GradePartial || as[0].TeacherGrade != classroom.GradeNone || as[0].AINote == "" {
		t.Fatalf("annotation must not override the teacher: %+v", as[0])
	}
	if res := f.call(t, "annotate_answers", map[string]any{"question_id": f.qid, "annotations": []map[string]any{{"student_id": f.lea, "grade": "A+"}}}, nil); !res.IsError {
		t.Fatal("bad grade accepted")
	}

	f.call(t, "highlight_student", map[string]any{"student_id": f.tom, "question_id": f.qid, "reason": "bloqué depuis 2 min"}, nil)
	found := false
	for _, a := range f.room.Alerts() {
		found = found || (a.Source == "claude" && a.StudentID == f.tom)
	}
	if !found {
		t.Fatal("highlight not recorded")
	}

	var m messageOut
	f.call(t, "message_student", map[string]any{"student_id": f.tom, "message": "Pense à la différence avec un thread OS."}, &m)
	if m.Status != "pending" || len(f.room.MessagesFor(f.tom)) != 0 {
		t.Fatalf("suggest mode must wait for approval: %+v", m)
	}
	_ = f.room.SetAIMode(classroom.AIModeAuto)
	f.call(t, "message_student", map[string]any{"student_id": f.tom, "message": "Indice 1"}, &m)
	f.call(t, "message_student", map[string]any{"student_id": f.tom, "message": "Indice 2"}, &m)
	res := f.call(t, "message_student", map[string]any{"student_id": f.tom, "message": "Indice 3"}, nil)
	if !res.IsError || !strings.Contains(errText(res), "limite") {
		t.Fatalf("auto mode limit not enforced: %s", errText(res))
	}
	if n := len(f.room.MessagesFor(f.tom)); n != 2 {
		t.Fatalf("messages delivered: %d", n)
	}
}

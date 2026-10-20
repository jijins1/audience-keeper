package classroom

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

type recNotifier struct {
	mu       sync.Mutex
	teacher  int
	students [][]string
}

func (r *recNotifier) TeacherDirty() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.teacher++
}

func (r *recNotifier) StudentsDirty(ids ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.students = append(r.students, ids)
}

func newTest(t *testing.T) (*Classroom, *fakeClock, *recNotifier) {
	t.Helper()
	clk := &fakeClock{t: time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)}
	n := &recNotifier{}
	return New(WithClock(clk.Now), WithNotifier(n), WithCode("ABC123")), clk, n
}

func mustJoin(t *testing.T, c *Classroom, pseudo string) string {
	t.Helper()
	s, tok, err := c.Join("ABC123", pseudo)
	if err != nil {
		t.Fatalf("join %q: %v", pseudo, err)
	}
	if id, ok := c.Authenticate(tok); !ok || id != s.ID {
		t.Fatalf("token does not authenticate %s", s.ID)
	}
	if err := c.Connect(s.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return s.ID
}

func mustOpen(t *testing.T, c *Classroom, typ QuestionType) Question {
	t.Helper()
	q, err := c.CreateQuestion(NewQuestion{Title: "Q", Type: typ, Open: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return q
}

func TestJoin(t *testing.T) {
	c, _, _ := newTest(t)
	mustJoin(t, c, "Léa")
	tests := []struct {
		name, code, pseudo string
		want               error
	}{
		{"bad code", "XXXXXX", "Tom", ErrBadCode},
		{"code is case insensitive", " abc123 ", "Tom", nil},
		{"empty pseudo", "ABC123", "   ", ErrBadPseudo},
		{"pseudo too long", "ABC123", strings.Repeat("a", MaxPseudoLen+1), ErrBadPseudo},
		{"control chars", "ABC123", "a\x00b", ErrBadPseudo},
		{"duplicate pseudo", "ABC123", "léa", ErrPseudoTaken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := c.Join(tt.code, tt.pseudo)
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
	if _, ok := c.Authenticate("nope"); ok {
		t.Fatal("unknown token authenticated")
	}
}

func TestConnectDisconnect(t *testing.T) {
	c, _, _ := newTest(t)
	id := mustJoin(t, c, "a")
	_ = c.Connect(id) // second tab
	c.Disconnect(id)
	if !c.Students()[0].Connected {
		t.Fatal("still one connection, should be connected")
	}
	c.Disconnect(id)
	if c.Students()[0].Connected {
		t.Fatal("should be disconnected")
	}
	if err := c.Connect("ghost"); !errors.Is(err, ErrUnknownStudent) {
		t.Fatalf("got %v", err)
	}
}

func TestCreateQuestionValidation(t *testing.T) {
	c, _, _ := newTest(t)
	tests := []struct {
		name string
		nq   NewQuestion
		want error
		n    int
	}{
		{"text", NewQuestion{Type: TypeText}, nil, 0},
		{"choice default 4", NewQuestion{Type: TypeChoice}, nil, 4},
		{"choice 3", NewQuestion{Type: TypeChoice, NumChoices: 3}, nil, 3},
		{"choice too many", NewQuestion{Type: TypeChoice, NumChoices: 9}, ErrBadChoices, 0},
		{"yesno", NewQuestion{Type: TypeYesNo}, nil, 2},
		{"bad type", NewQuestion{Type: "essay"}, ErrBadType, 0},
		{"title too long", NewQuestion{Type: TypeText, Title: strings.Repeat("x", MaxTitleLen+1)}, ErrTooLong, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := c.CreateQuestion(tt.nq)
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if err == nil && len(q.Choices) != tt.n {
				t.Fatalf("choices = %v", q.Choices)
			}
			if err == nil && q.Status != StatusProposed {
				t.Fatalf("status = %s", q.Status)
			}
		})
	}
	if qs := c.Questions(); qs[0].Title != "Question 1" {
		t.Fatalf("default title = %q", qs[0].Title)
	}
}

func TestOpenCloseLifecycle(t *testing.T) {
	c, _, n := newTest(t)
	sid := mustJoin(t, c, "a")
	q1 := mustOpen(t, c, TypeText)
	if cur, ok := c.Current(); !ok || cur.ID != q1.ID {
		t.Fatal("q1 should be current")
	}
	if err := c.UpdateDraft(sid, q1.ID, "draft answer"); err != nil {
		t.Fatal(err)
	}
	q2, _ := c.CreateQuestion(NewQuestion{Type: TypeYesNo})
	if err := c.OpenQuestion(q2.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := c.Question(q1.ID)
	if got.Status != StatusClosed {
		t.Fatalf("opening q2 should close q1, got %s", got.Status)
	}
	ans, _ := c.Answers(q1.ID)
	if !ans[0].Submitted || !ans[0].AutoSubmitted || ans[0].Value != "draft answer" {
		t.Fatalf("draft should be auto-submitted: %+v", ans[0])
	}
	if err := c.OpenQuestion(q1.ID); !errors.Is(err, ErrBadStatus) {
		t.Fatalf("reopen closed: %v", err)
	}
	if err := c.CloseQuestion(q2.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseQuestion(q2.ID); !errors.Is(err, ErrBadStatus) {
		t.Fatalf("double close: %v", err)
	}
	if err := c.CloseQuestion("nope"); !errors.Is(err, ErrUnknownQuestion) {
		t.Fatal(err)
	}
	if _, ok := c.Current(); ok {
		t.Fatal("no question should be open")
	}
	if n.teacher == 0 || len(n.students) == 0 {
		t.Fatal("notifier not called")
	}
}

func TestDeleteQuestion(t *testing.T) {
	c, _, _ := newTest(t)
	q, _ := c.CreateQuestion(NewQuestion{Type: TypeText, ProposedBy: "claude"})
	if q.ProposedBy != "claude" {
		t.Fatal(q.ProposedBy)
	}
	if err := c.DeleteQuestion(q.ID); err != nil {
		t.Fatal(err)
	}
	if len(c.Questions()) != 0 {
		t.Fatal("not deleted")
	}
	open := mustOpen(t, c, TypeText)
	if err := c.DeleteQuestion(open.ID); !errors.Is(err, ErrBadStatus) {
		t.Fatal(err)
	}
	if err := c.DeleteQuestion("x"); !errors.Is(err, ErrUnknownQuestion) {
		t.Fatal(err)
	}
}

func TestSubmitValidation(t *testing.T) {
	c, _, _ := newTest(t)
	sid := mustJoin(t, c, "a")
	tests := []struct {
		typ   QuestionType
		value string
		want  error
		norm  string
	}{
		{TypeChoice, "b", nil, "B"},
		{TypeChoice, "Z", ErrBadAnswer, ""},
		{TypeYesNo, "oui", nil, "Oui"},
		{TypeNumber, "3,5", nil, "3.5"},
		{TypeNumber, "NaN", ErrBadAnswer, ""},
		{TypeNumber, "abc", ErrBadAnswer, ""},
		{TypeText, "  hello  ", nil, "hello"},
		{TypeText, "   ", ErrEmpty, ""},
		{TypeText, strings.Repeat("x", MaxAnswerLen+1), ErrTooLong, ""},
	}
	for _, tt := range tests {
		t.Run(string(tt.typ)+"/"+tt.value[:min(len(tt.value), 8)], func(t *testing.T) {
			q := mustOpen(t, c, tt.typ)
			err := c.Submit(sid, q.ID, tt.value)
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if err == nil {
				a, _ := c.Answers(q.ID)
				if a[0].Value != tt.norm {
					t.Fatalf("value %q, want %q", a[0].Value, tt.norm)
				}
				if err := c.Submit(sid, q.ID, tt.value); !errors.Is(err, ErrAlreadyAnswered) {
					t.Fatalf("second submit: %v", err)
				}
				if err := c.UpdateDraft(sid, q.ID, "x"); !errors.Is(err, ErrAlreadyAnswered) {
					t.Fatalf("draft after submit: %v", err)
				}
			}
		})
	}
}

func TestAnswerRequiresOpenQuestion(t *testing.T) {
	c, _, _ := newTest(t)
	sid := mustJoin(t, c, "a")
	q, _ := c.CreateQuestion(NewQuestion{Type: TypeText})
	if err := c.UpdateDraft(sid, q.ID, "x"); !errors.Is(err, ErrNotOpen) {
		t.Fatal(err)
	}
	if err := c.Submit("ghost", q.ID, "x"); !errors.Is(err, ErrUnknownStudent) {
		t.Fatal(err)
	}
	if err := c.RecordEvent(sid, "nope", ClientEvent{Kind: EventPaste}); !errors.Is(err, ErrUnknownQuestion) {
		t.Fatal(err)
	}
}

func TestPasteBurstAndBlur(t *testing.T) {
	c, clk, _ := newTest(t)
	sid := mustJoin(t, c, "a")
	q := mustOpen(t, c, TypeText)
	long := strings.Repeat("x", 40)

	// paste then large insert: attributed to the paste, no burst
	if err := c.RecordEvent(sid, q.ID, ClientEvent{Kind: EventPaste, Chars: 40, Text: long}); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateDraft(sid, q.ID, long); err != nil {
		t.Fatal(err)
	}
	// large insert long after: burst
	clk.Advance(5 * time.Second)
	if err := c.UpdateDraft(sid, q.ID, long+long); err != nil {
		t.Fatal(err)
	}
	if err := c.RecordEvent(sid, q.ID, ClientEvent{Kind: EventBlur}); err != nil {
		t.Fatal(err)
	}
	_ = c.RecordEvent(sid, q.ID, ClientEvent{Kind: EventBlur}) // duplicate blur ignored
	clk.Advance(12 * time.Second)
	if err := c.RecordEvent(sid, q.ID, ClientEvent{Kind: EventFocus}); err != nil {
		t.Fatal(err)
	}
	_ = c.RecordEvent(sid, q.ID, ClientEvent{Kind: EventCopy, Chars: 3})

	a, _ := c.Answers(q.ID)
	got := a[0]
	if got.Pastes != 1 || got.PastedChars != 40 || got.Bursts != 1 || got.Blurs != 1 || got.BlurMs != 12000 || got.Copies != 1 || got.Hidden {
		t.Fatalf("signals: %+v", got)
	}
	kinds := map[string]bool{}
	for _, al := range c.Alerts() {
		kinds[al.Kind] = true
	}
	if !kinds["paste"] || !kinds["burst"] {
		t.Fatalf("alerts: %v", kinds)
	}
	if err := c.RecordEvent(sid, q.ID, ClientEvent{Kind: EventSubmit}); !errors.Is(err, ErrBadEvent) {
		t.Fatal("client must not send submit events")
	}
	if err := c.RecordEvent(sid, q.ID, ClientEvent{Kind: EventPaste, Chars: -1}); !errors.Is(err, ErrBadEvent) {
		t.Fatal("negative chars accepted")
	}
	ch := c.ChangesSince(0)
	if len(ch.Events) == 0 || len(ch.Answers) != 1 || len(ch.Alerts) < 2 {
		t.Fatalf("changes: %+v", ch)
	}
	if later := c.ChangesSince(ch.Cursor); len(later.Events)+len(later.Answers)+len(later.Alerts) != 0 {
		t.Fatalf("nothing should be new: %+v", later)
	}
}

func TestApplyRules(t *testing.T) {
	c, clk, _ := newTest(t)
	idle := mustJoin(t, c, "idle")
	stuck := mustJoin(t, c, "stuck")
	away := mustJoin(t, c, "away")
	done := mustJoin(t, c, "done")
	gone := mustJoin(t, c, "gone")
	c.Disconnect(gone)
	q := mustOpen(t, c, TypeText)
	_ = c.UpdateDraft(stuck, q.ID, "je sais pas")
	_ = c.UpdateDraft(away, q.ID, "x")
	_ = c.RecordEvent(away, q.ID, ClientEvent{Kind: EventBlur})
	_ = c.Submit(done, q.ID, "ok")

	c.ApplyRules(DefaultThresholds)
	if len(c.Alerts()) != 0 {
		t.Fatalf("too early: %+v", c.Alerts())
	}
	clk.Advance(100 * time.Second)
	_ = c.UpdateDraft(away, q.ID, "xy") // still typing, but page hidden
	c.ApplyRules(DefaultThresholds)
	c.ApplyRules(DefaultThresholds) // dedup
	got := map[string]string{}
	for _, a := range c.Alerts() {
		got[a.StudentID+":"+a.Kind] = a.Source
	}
	want := []string{idle + ":inactive", stuck + ":stalled", away + ":hidden"}
	for _, k := range want {
		if got[k] != "rules" {
			t.Errorf("missing alert %s in %v", k, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected alerts: %v", got)
	}
}

func TestGradesAndStats(t *testing.T) {
	c, clk, _ := newTest(t)
	ids := []string{mustJoin(t, c, "a"), mustJoin(t, c, "b"), mustJoin(t, c, "c"), mustJoin(t, c, "d")}
	q := mustOpen(t, c, TypeChoice)
	clk.Advance(10 * time.Second)
	_ = c.Submit(ids[0], q.ID, "A")
	clk.Advance(10 * time.Second)
	_ = c.Submit(ids[1], q.ID, "A")
	_ = c.Submit(ids[2], q.ID, "C")
	_ = c.UpdateDraft(ids[3], q.ID, "B")

	if err := c.SetCorrect(q.ID, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	s, err := c.Stats(q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.Answered != 3 || s.Typing != 1 || s.ParticipationPct != 75 || s.Correct != 2 || s.Wrong != 1 {
		t.Fatalf("stats: %+v", s)
	}
	if s.Distribution[0].Count != 2 || !s.Distribution[0].Correct || s.Distribution[2].Count != 1 {
		t.Fatalf("distribution: %+v", s.Distribution)
	}
	if s.AvgResponseMs != 16666 || s.MedianResponseMs != 20000 {
		t.Fatalf("times: avg %d median %d", s.AvgResponseMs, s.MedianResponseMs)
	}
	// teacher overrides automatic correction
	if err := c.Grade(q.ID, ids[2], GradePartial, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Grade(q.ID, ids[0], GradeCorrect, false, "bien vu"); err != nil {
		t.Fatal(err)
	}
	if err := c.Grade(q.ID, ids[0], "great", false, ""); !errors.Is(err, ErrBadGrade) {
		t.Fatal(err)
	}
	s, _ = c.Stats(q.ID)
	if s.Partial != 1 || s.Wrong != 0 || *s.CorrectPct != 66.7 {
		t.Fatalf("after grade: %+v", s)
	}
	if err := c.SetCorrect(q.ID, []string{"Z"}); !errors.Is(err, ErrBadAnswer) {
		t.Fatal(err)
	}
}

func TestTextAndNumberStats(t *testing.T) {
	c, _, _ := newTest(t)
	ids := []string{mustJoin(t, c, "a"), mustJoin(t, c, "b"), mustJoin(t, c, "c")}
	q := mustOpen(t, c, TypeText)
	_ = c.Submit(ids[0], q.ID, "Une goroutine légère.")
	_ = c.Submit(ids[1], q.ID, "une   goroutine légère")
	_ = c.Submit(ids[2], q.ID, "un thread")
	_ = c.SetCorrect(q.ID, []string{"une goroutine légère"})
	s, _ := c.Stats(q.ID)
	if len(s.TextGroups) != 2 || s.TextGroups[0].Count != 2 || !s.TextGroups[0].Correct {
		t.Fatalf("groups: %+v", s.TextGroups)
	}
	if s.Words[0].Word != "goroutine" || s.Words[0].Count != 2 {
		t.Fatalf("words: %+v", s.Words)
	}

	n := mustOpen(t, c, TypeNumber)
	for i, v := range []string{"1", "2", "10"} {
		_ = c.Submit(ids[i], n.ID, v)
	}
	s, _ = c.Stats(n.ID)
	if s.Numeric == nil || s.Numeric.Min != 1 || s.Numeric.Max != 10 || s.Numeric.Median != 2 || len(s.Numeric.Histogram) != 3 {
		t.Fatalf("numeric: %+v", s.Numeric)
	}
	total := 0
	for _, b := range s.Numeric.Histogram {
		total += b.Count
	}
	if total != 3 {
		t.Fatalf("histogram loses values: %+v", s.Numeric.Histogram)
	}
	if _, err := c.Stats("nope"); !errors.Is(err, ErrUnknownQuestion) {
		t.Fatal(err)
	}
}

func TestHistogramSingleValue(t *testing.T) {
	h := histogram([]float64{4, 4}, 4, 4)
	if len(h) != 1 || h[0].Count != 2 {
		t.Fatalf("%+v", h)
	}
}

func TestGroups(t *testing.T) {
	c, _, _ := newTest(t)
	a := mustJoin(t, c, "a")
	q := mustOpen(t, c, TypeText)
	if err := c.SetGroups(q.ID, []Group{{Label: " Confusion ", StudentIDs: []string{a}}}); err != nil {
		t.Fatal(err)
	}
	got, _ := c.Question(q.ID)
	if got.Groups[0].Label != "Confusion" {
		t.Fatalf("%+v", got.Groups)
	}
	if err := c.SetGroups(q.ID, []Group{{Label: "x", StudentIDs: []string{"ghost"}}}); !errors.Is(err, ErrUnknownStudent) {
		t.Fatal(err)
	}
	if err := c.SetGroups(q.ID, []Group{{Label: ""}}); !errors.Is(err, ErrEmpty) {
		t.Fatal(err)
	}
	// returned copies must not alias internal state
	got.Groups[0].StudentIDs[0] = "mutated"
	again, _ := c.Question(q.ID)
	if again.Groups[0].StudentIDs[0] != a {
		t.Fatal("Question() leaked internal slice")
	}
}

func TestSuggestionsSuggestMode(t *testing.T) {
	c, _, _ := newTest(t)
	a := mustJoin(t, c, "a")
	s, err := c.SuggestMessage(a, "Regarde la ligne 3")
	if err != nil || s.Status != SuggestionPending {
		t.Fatalf("%+v %v", s, err)
	}
	if len(c.MessagesFor(a)) != 0 {
		t.Fatal("suggest mode must not send")
	}
	if err := c.ApproveSuggestion(s.ID, "Regarde bien la ligne 3"); err != nil {
		t.Fatal(err)
	}
	msgs := c.MessagesFor(a)
	if len(msgs) != 1 || !msgs[0].FromAI || msgs[0].Text != "Regarde bien la ligne 3" {
		t.Fatalf("%+v", msgs)
	}
	if err := c.ApproveSuggestion(s.ID, ""); !errors.Is(err, ErrBadStatus) {
		t.Fatal(err)
	}
	s2, _ := c.SuggestMessage(a, "autre")
	if err := c.DismissSuggestion(s2.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DismissSuggestion("nope"); !errors.Is(err, ErrUnknownSuggest) {
		t.Fatal(err)
	}
	if _, err := c.SuggestMessage("ghost", "x"); !errors.Is(err, ErrUnknownStudent) {
		t.Fatal(err)
	}
	if _, err := c.SuggestMessage(a, " "); !errors.Is(err, ErrEmpty) {
		t.Fatal(err)
	}
	if len(c.Suggestions()) != 2 {
		t.Fatal("suggestions not kept")
	}
}

func TestSuggestionsAutoModeLimit(t *testing.T) {
	c, _, _ := newTest(t)
	a := mustJoin(t, c, "a")
	mustOpen(t, c, TypeText)
	if err := c.SetAIMode("yolo"); !errors.Is(err, ErrBadMode) {
		t.Fatal(err)
	}
	if err := c.SetAIMode(AIModeAuto); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxAutoMessagesPerQuestion; i++ {
		s, err := c.SuggestMessage(a, "indice")
		if err != nil || s.Status != SuggestionSent {
			t.Fatalf("%d: %+v %v", i, s, err)
		}
	}
	if _, err := c.SuggestMessage(a, "encore"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("limit not enforced: %v", err)
	}
	if _, err := c.SendMessage(a, "message du prof", false); err != nil {
		t.Fatal("teacher messages are not limited")
	}
	if len(c.MessagesFor(a)) != 3 {
		t.Fatal("messages missing")
	}
}

func TestHighlight(t *testing.T) {
	c, _, _ := newTest(t)
	a := mustJoin(t, c, "a")
	if err := c.Highlight(a, "", "confond X et Y"); err != nil {
		t.Fatal(err)
	}
	if err := c.Highlight(a, "", "encore"); err != nil {
		t.Fatal(err)
	}
	if len(c.Alerts()) != 2 {
		t.Fatal("claude alerts must not be deduplicated")
	}
	if err := c.Highlight(a, "nope", "x"); !errors.Is(err, ErrUnknownQuestion) {
		t.Fatal(err)
	}
	if err := c.Highlight(a, "", ""); !errors.Is(err, ErrEmpty) {
		t.Fatal(err)
	}
}

func TestViews(t *testing.T) {
	c, _, _ := newTest(t)
	if b, _ := json.Marshal(c.TeacherView("")); !strings.Contains(string(b), `"questions":[]`) || !strings.Contains(string(b), `"students":[]`) {
		t.Fatalf("empty lists must serialise as [] for the dashboard: %s", b)
	}
	a := mustJoin(t, c, "Léa")
	sv, _ := c.StudentView(a)
	if sv.Question != nil || sv.Pseudo != "Léa" {
		t.Fatalf("%+v", sv)
	}
	q := mustOpen(t, c, TypeYesNo)
	_ = c.Submit(a, q.ID, "non")
	sv, _ = c.StudentView(a)
	if sv.Question == nil || sv.Question.ID != q.ID || !sv.Submitted || sv.Value != "Non" {
		t.Fatalf("%+v", sv)
	}
	if _, err := c.StudentView("ghost"); !errors.Is(err, ErrUnknownStudent) {
		t.Fatal(err)
	}
	tv := c.TeacherView("")
	if tv.Code != "ABC123" || tv.CurrentID != q.ID || tv.View == nil || tv.View.Stats.Answered != 1 || tv.Questions[0].Answered != 1 {
		t.Fatalf("%+v", tv)
	}
	_ = c.CloseQuestion(q.ID)
	if tv := c.TeacherView("unknown"); tv.View == nil || tv.View.Question.ID != q.ID {
		t.Fatal("should fall back to latest question")
	}
	_, hist, err := c.StudentHistory(a)
	if err != nil || len(hist) != 1 || hist[0].Answer == nil {
		t.Fatalf("%+v %v", hist, err)
	}
	sum := c.Summary()
	if len(sum) != 1 || sum[0].Answered != 1 {
		t.Fatalf("%+v", sum)
	}
}

func TestExportCSV(t *testing.T) {
	c, _, _ := newTest(t)
	a := mustJoin(t, c, "=cmd")
	q := mustOpen(t, c, TypeText)
	_ = c.Submit(a, q.ID, "=HYPERLINK(\"x\")")
	var buf bytes.Buffer
	if err := c.ExportCSV(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "'=cmd") || !strings.Contains(out, "'=HYPERLINK") {
		t.Fatalf("formula injection not neutralised:\n%s", out)
	}
	if strings.Count(out, "\n") != 2 {
		t.Fatalf("expected header + 1 row:\n%s", out)
	}
}

func TestReset(t *testing.T) {
	c := New()
	code := c.Code()
	_, tok, _ := c.Join(code, "a")
	c.Reset()
	if c.Code() == code && len(code) != 6 {
		t.Fatal("bad code")
	}
	if _, ok := c.Authenticate(tok); ok {
		t.Fatal("old tokens must be invalid after reset")
	}
	if !c.StartedAt().After(time.Time{}) {
		t.Fatal("startedAt not set")
	}
}

func TestConcurrentAccess(t *testing.T) {
	c, _, _ := newTest(t)
	q := mustOpen(t, c, TypeText)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		id := mustJoin(t, c, "p"+string(rune('a'+i)))
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = c.UpdateDraft(id, q.ID, strings.Repeat("x", j))
				_ = c.TeacherView("")
			}
			_ = c.Submit(id, q.ID, "fin")
		}()
	}
	wg.Wait()
	if s, _ := c.Stats(q.ID); s.Answered != 20 {
		t.Fatalf("answered = %d", s.Answered)
	}
}

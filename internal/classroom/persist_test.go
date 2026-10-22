package classroom

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	c, clk, _ := newTest(t)
	lea, leaTok, err := c.Join("ABC123", "Léa")
	if err != nil {
		t.Fatal(err)
	}
	tom := mustJoin(t, c, "Tom")
	if err := c.Connect(lea.ID); err != nil {
		t.Fatal(err)
	}
	q, _ := c.CreateQuestion(NewQuestion{Title: "total()", Type: TypeCode, Open: true, Key: "slide:1",
		Code: &CodeInput{Context: "class A {}", Template: "class B {\n@@\n}"}})
	_ = c.RecordEvent(lea.ID, q.ID, ClientEvent{Kind: EventPaste, Chars: 12, Text: "collé"})
	_ = c.UpdateDraft(tom, q.ID, "return 0;")
	if err := c.Submit(lea.ID, q.ID, "return 1;"); err != nil {
		t.Fatal(err)
	}
	_ = c.SetAIMode(AIModeSuggest)
	if _, err := c.SuggestMessage(tom, "Pense au return"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendMessage(lea.ID, "Bravo", false); err != nil {
		t.Fatal(err)
	}
	_ = c.SetGroups(q.ID, []Group{{Label: "Bonne idée", StudentIDs: []string{lea.ID}}})
	clk.Advance(5e9)

	data, rev, err := c.MarshalState()
	if err != nil || rev != c.Revision() {
		t.Fatalf("marshal: %v (rev %d / %d)", err, rev, c.Revision())
	}
	if strings.Contains(string(data), leaTok) {
		t.Fatal("student tokens must only be saved hashed")
	}

	r := New(WithClock(clk.Now))
	before := r.Revision()
	if err := r.RestoreState(data); err != nil {
		t.Fatal(err)
	}
	if r.Revision() == before {
		t.Fatal("a restore is a change: the saver must see it")
	}
	if r.Code() != "ABC123" {
		t.Fatalf("join code lost: %s", r.Code())
	}
	if id, ok := r.Authenticate(leaTok); !ok || id != lea.ID {
		t.Fatal("a student must get back in with the token kept by their page")
	}
	for _, s := range r.Students() {
		if s.Connected {
			t.Fatalf("%s restored as connected", s.Pseudo)
		}
	}
	// what the teacher and Claude see is the same
	want, got := c.TeacherView(q.ID), r.TeacherView(q.ID)
	for i := range want.Students {
		want.Students[i].Connected = false
	}
	want.Live, got.Live = nil, nil
	if !reflect.DeepEqual(mustJSON(t, want), mustJSON(t, got)) {
		t.Fatalf("teacher view differs after restore:\n%s\n%s", mustJSON(t, want), mustJSON(t, got))
	}
	sv, _ := r.StudentView(lea.ID)
	if !sv.Submitted || sv.Value != "return 1;" || len(sv.Messages) != 1 || sv.Question.Code == nil {
		t.Fatalf("student view after restore: %+v", sv)
	}
	if ch := r.ChangesSince(0); len(ch.Events) == 0 {
		t.Fatal("events lost")
	}

	// the session goes on: no ID is reused, idempotent keys still match
	s3, _, err := r.Join("ABC123", "Ana")
	if err != nil || s3.ID == lea.ID || s3.ID == tom {
		t.Fatalf("new student after restore: %+v %v", s3, err)
	}
	if _, _, err := r.Join("ABC123", "léa"); !errors.Is(err, ErrPseudoTaken) {
		t.Fatal("pseudos taken before the restart stay taken")
	}
	again, _ := r.CreateQuestion(NewQuestion{Type: TypeCode, Open: true, Key: "slide:1"})
	if again.ID != q.ID {
		t.Fatal("the slide's question must be found again by its key")
	}
	// the paste alert is not raised twice
	alerts := len(r.Alerts())
	_ = r.RecordEvent(tom, q.ID, ClientEvent{Kind: EventBlur})
	if len(r.Alerts()) < alerts {
		t.Fatal("alerts lost")
	}
}

func TestRestoreRejectsBadState(t *testing.T) {
	c, _, _ := newTest(t)
	mustJoin(t, c, "Léa")
	good, _, _ := c.MarshalState()

	var tampered map[string]any
	_ = json.Unmarshal(good, &tampered)
	tampered["answers"] = []map[string]any{{"questionId": "q404", "studentId": "s1"}}
	orphan, _ := json.Marshal(tampered)
	tampered["version"] = 99
	future, _ := json.Marshal(tampered)

	for name, data := range map[string][]byte{"not json": []byte("{"), "orphan answer": orphan, "future version": future} {
		r, _, _ := newTest(t)
		mustJoin(t, r, "Tom")
		if err := r.RestoreState(data); !errors.Is(err, ErrBadState) {
			t.Fatalf("%s: %v", name, err)
		}
		if len(r.Students()) != 1 || r.Students()[0].Pseudo != "Tom" {
			t.Fatalf("%s: a failed restore must leave the session untouched", name)
		}
	}
}

func TestRevisionTracksChanges(t *testing.T) {
	c, _, _ := newTest(t)
	r0 := c.Revision()
	sid := mustJoin(t, c, "Léa")
	r1 := c.Revision()
	if r1 == r0 {
		t.Fatal("join must bump the revision")
	}
	_ = c.Code()
	_ = c.TeacherView("")
	if c.Revision() != r1 {
		t.Fatal("reads must not bump the revision")
	}
	q := mustOpen(t, c, TypeText)
	r2 := c.Revision()
	_ = c.UpdateDraft(sid, q.ID, "a")
	if c.Revision() == r2 {
		t.Fatal("drafts must be saved too")
	}
	r3 := c.Revision()
	c.Reset()
	if c.Revision() == r3 {
		t.Fatal("reset must bump the revision")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

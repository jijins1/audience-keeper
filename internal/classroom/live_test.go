package classroom

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestLiveSlide(t *testing.T) {
	c, _, n := newTest(t)
	a := mustJoin(t, c, "a")
	if sv, _ := c.StudentView(a); sv.Slide != nil {
		t.Fatal("no slide yet")
	}
	before := len(n.students)
	ok := LiveSlide{Deck: "Cours", HTML: "<section><h2>Hello</h2></section>", H: 2, Fragment: -1,
		Themes: []string{"/static/vendor/reveal/reveal.css", "/deck/custom.css", "https://cdn.jsdelivr.net/npm/reveal.js/dist/theme/black.css"}}
	if err := c.SetSlide(ok, PresenterInfo{}); err != nil {
		t.Fatal(err)
	}
	if len(n.students) == before {
		t.Fatal("students must be refreshed on slide change")
	}
	sv, _ := c.StudentView(a)
	if sv.Slide == nil || sv.Slide.H != 2 || sv.Slide.Rev == 0 || len(sv.Slide.Themes) != 3 {
		t.Fatalf("%+v", sv.Slide)
	}
	if tv := c.TeacherView(""); tv.Live == nil || tv.Live.Deck != "Cours" {
		t.Fatalf("%+v", tv.Live)
	}
	rev := sv.Slide.Rev
	ok.Fragment = 1
	_ = c.SetSlide(ok, PresenterInfo{})
	if s, _ := c.Slide(); s.Rev <= rev || s.Fragment != 1 {
		t.Fatalf("revision must grow: %+v", s)
	}

	bad := []LiveSlide{
		{HTML: strings.Repeat("x", MaxSlideHTML+1)},
		{Fragment: -2},
		{H: -1},
		{Themes: []string{"https://evil.example/x.css"}},
		{Themes: []string{"/static/../secret"}},
		{Themes: []string{"//evil.example/x.css"}},
		{Themes: make([]string, maxThemes+1)},
	}
	for i, b := range bad {
		if err := c.SetSlide(b, PresenterInfo{}); err == nil {
			t.Errorf("bad slide %d accepted", i)
		} else if !errors.Is(err, ErrTooLong) && !errors.Is(err, ErrBadEvent) {
			t.Errorf("bad slide %d: unexpected %v", i, err)
		}
	}
	c.ClearSlide()
	if _, ok := c.Slide(); ok {
		t.Fatal("cleared")
	}
	_ = c.SetSlide(ok, PresenterInfo{})
	c.Reset()
	if _, ok := c.Slide(); ok {
		t.Fatal("reset must stop mirroring")
	}
}

func TestStudentViewHidesMessageOrigin(t *testing.T) {
	c, _, _ := newTest(t)
	a := mustJoin(t, c, "a")
	_, _ = c.SendMessage(a, "du prof", false)
	_ = c.SetAIMode(AIModeAuto)
	_, _ = c.SuggestMessage(a, "de Claude")
	sv, _ := c.StudentView(a)
	b, _ := json.Marshal(sv)
	if len(sv.Messages) != 2 || strings.Contains(strings.ToLower(string(b)), "fromai") {
		t.Fatalf("student payload must not reveal message origin: %s", b)
	}
	if msgs := c.MessagesFor(a); !msgs[1].FromAI {
		t.Fatal("the origin must still be recorded for the teacher")
	}
}

func TestPresenterInfoNeverReachesStudents(t *testing.T) {
	c, _, _ := newTest(t)
	a := mustJoin(t, c, "a")
	info := PresenterInfo{Notes: "NOTE-SECRETE", NextHTML: "<section>SLIDE-SUIVANTE</section>", Number: 3, Total: 9, Fragments: 2, QuestionKey: "q-x"}
	if err := c.SetSlide(LiveSlide{HTML: "<section>courante</section>", Fragment: -1}, info); err != nil {
		t.Fatal(err)
	}
	sv, _ := c.StudentView(a)
	b, _ := json.Marshal(sv)
	if strings.Contains(string(b), "NOTE-SECRETE") || strings.Contains(string(b), "SLIDE-SUIVANTE") {
		t.Fatalf("teacher-only details leaked to a student: %s", b)
	}
	s, p, ok := c.Presenter()
	if !ok || p != info || s.HTML != "<section>courante</section>" {
		t.Fatalf("%+v %+v %v", s, p, ok)
	}
	for i, bad := range []PresenterInfo{
		{Notes: strings.Repeat("n", MaxNotesHTML+1)},
		{NextHTML: strings.Repeat("x", MaxSlideHTML+1)},
		{Number: 5, Total: 3},
		{Fragments: -1},
	} {
		if err := c.SetSlide(LiveSlide{Fragment: -1}, bad); err == nil {
			t.Errorf("bad presenter info %d accepted", i)
		}
	}
	c.ClearSlide()
	if _, _, ok := c.Presenter(); ok {
		t.Fatal("cleared")
	}
}

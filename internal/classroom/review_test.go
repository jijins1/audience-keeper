package classroom

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestHugeNumbersDoNotBreakStats(t *testing.T) {
	c, _, _ := newTest(t)
	a, b := mustJoin(t, c, "a"), mustJoin(t, c, "b")
	q := mustOpen(t, c, TypeNumber)
	if err := c.Submit(a, q.ID, "1.7e308"); !errors.Is(err, ErrBadAnswer) {
		t.Fatalf("out-of-range number accepted: %v", err)
	}
	_ = c.Submit(a, q.ID, "1e15")
	_ = c.Submit(b, q.ID, "-1e15")
	s, _ := c.Stats(q.ID)
	if _, err := json.Marshal(s); err != nil {
		t.Fatalf("stats must stay serialisable: %v", err)
	}
	if s.Numeric.Mean != 0 || len(s.Numeric.Histogram) != 2 {
		t.Fatalf("%+v", s.Numeric)
	}
}

func TestChangesSincePagesWithoutLoss(t *testing.T) {
	c, _, _ := newTest(t)
	a := mustJoin(t, c, "a")
	q := mustOpen(t, c, TypeText)
	for i := 0; i < maxChanges+20; i++ {
		_ = c.RecordEvent(a, q.ID, ClientEvent{Kind: EventCopy})
	}
	seen := map[int64]bool{}
	cursor := int64(0)
	for i := 0; i < 5; i++ {
		ch := c.ChangesSince(cursor)
		for _, e := range ch.Events {
			seen[e.Seq] = true
		}
		cursor = ch.Cursor
		if !ch.More {
			break
		}
	}
	if len(seen) != len(c.events) {
		t.Fatalf("events lost: got %d of %d", len(seen), len(c.events))
	}
}

func TestAIGradeKeptOnNoteOnly(t *testing.T) {
	c, _, _ := newTest(t)
	a := mustJoin(t, c, "a")
	q := mustOpen(t, c, TypeText)
	_ = c.Grade(q.ID, a, GradeCorrect, false, "")
	_ = c.Grade(q.ID, a, GradeNone, false, "juste une note")
	as, _ := c.Answers(q.ID)
	if as[0].AIGrade != GradeCorrect || as[0].AINote != "juste une note" {
		t.Fatalf("%+v", as[0])
	}
}

func TestCorrectPctOverGradedAndAutoTiming(t *testing.T) {
	c, clk, _ := newTest(t)
	ids := []string{mustJoin(t, c, "a"), mustJoin(t, c, "b"), mustJoin(t, c, "c")}
	q := mustOpen(t, c, TypeText)
	clk.Advance(10 * time.Second)
	_ = c.Submit(ids[0], q.ID, "x")
	_ = c.Submit(ids[1], q.ID, "y")
	_ = c.UpdateDraft(ids[2], q.ID, "z")
	clk.Advance(10 * time.Minute)
	_ = c.CloseQuestion(q.ID)
	_ = c.Grade(q.ID, ids[0], GradeCorrect, true, "")
	s, _ := c.Stats(q.ID)
	if s.CorrectPct == nil || *s.CorrectPct != 100 {
		t.Fatalf("1 graded, 1 correct => 100%%, got %v", s.CorrectPct)
	}
	if s.MedianResponseMs != 10000 || s.AvgResponseMs != 10000 {
		t.Fatalf("auto-submitted answers must not count in timing: %+v", s)
	}
	var buf bytes.Buffer
	_ = c.ExportCSV(&buf)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if !strings.Contains(lines[3], ",true,true,,") {
		t.Fatalf("auto row must have no response time: %s", lines[3])
	}
}

func TestCSVKeepsNegativeNumbers(t *testing.T) {
	c, _, _ := newTest(t)
	a := mustJoin(t, c, "a")
	q := mustOpen(t, c, TypeNumber)
	_ = c.Submit(a, q.ID, "-5")
	var buf bytes.Buffer
	_ = c.ExportCSV(&buf)
	if !strings.Contains(buf.String(), ",-5,") {
		t.Fatalf("%s", buf.String())
	}
}

func TestCaps(t *testing.T) {
	c, _, _ := newTest(t)
	for i := 0; i < MaxStudents; i++ {
		if _, _, err := c.Join("ABC123", fmt.Sprintf("p%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := c.Join("ABC123", "one-too-many"); !errors.Is(err, ErrFull) {
		t.Fatalf("got %v", err)
	}
	c2, _, _ := newTest(t)
	a := mustJoin(t, c2, "a")
	for i := 0; i < MaxPending; i++ {
		if _, err := c2.SuggestMessage(a, "x"); err != nil {
			t.Fatal(err)
		}
		if _, err := c2.CreateQuestion(NewQuestion{Type: TypeText, ProposedBy: "claude"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c2.SuggestMessage(a, "x"); !errors.Is(err, ErrTooManyPending) {
		t.Fatal(err)
	}
	if _, err := c2.CreateQuestion(NewQuestion{Type: TypeText, ProposedBy: "claude"}); !errors.Is(err, ErrTooManyPending) {
		t.Fatal(err)
	}
	if _, err := c2.CreateQuestion(NewQuestion{Type: TypeText}); err != nil {
		t.Fatal("the teacher is never capped")
	}
}

func TestInactiveCountsFromConnection(t *testing.T) {
	c, clk, _ := newTest(t)
	q := mustOpen(t, c, TypeText)
	clk.Advance(5 * time.Minute)
	mustJoin(t, c, "late")
	c.ApplyRules(DefaultThresholds)
	if len(c.Alerts()) != 0 {
		t.Fatalf("late joiner flagged immediately: %+v", c.Alerts())
	}
	clk.Advance(DefaultThresholds.Inactive)
	c.ApplyRules(DefaultThresholds)
	if len(c.Alerts()) != 1 || c.Alerts()[0].QuestionID != q.ID {
		t.Fatalf("%+v", c.Alerts())
	}
}

func TestTeacherViewTruncatesDrafts(t *testing.T) {
	c, _, _ := newTest(t)
	a := mustJoin(t, c, "a")
	q := mustOpen(t, c, TypeText)
	_ = c.UpdateDraft(a, q.ID, strings.Repeat("é", MaxAnswerLen))
	tv := c.TeacherView("")
	if n := len([]rune(tv.View.Answers[0].Draft)); n != teacherDraftLen {
		t.Fatalf("draft len %d", n)
	}
	full, _ := c.Answers(q.ID)
	if len([]rune(full[0].Draft)) != MaxAnswerLen {
		t.Fatal("stored draft must stay complete")
	}
}

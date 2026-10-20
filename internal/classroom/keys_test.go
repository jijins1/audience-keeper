package classroom

import (
	"errors"
	"strings"
	"testing"
)

func TestQuestionKeyIsIdempotent(t *testing.T) {
	c, _, _ := newTest(t)
	q1, err := c.CreateQuestion(NewQuestion{Key: "slide:3.0", Title: "Q", Type: TypeText, Open: true})
	if err != nil || q1.Status != StatusOpen || q1.Key != "slide:3.0" {
		t.Fatalf("%+v %v", q1, err)
	}
	again, _ := c.CreateQuestion(NewQuestion{Key: "slide:3.0", Title: "autre", Type: TypeChoice, Open: true})
	if again.ID != q1.ID || len(c.Questions()) != 1 {
		t.Fatal("same key must reuse the question")
	}
	_ = c.CloseQuestion(q1.ID)
	back, _ := c.CreateQuestion(NewQuestion{Key: "slide:3.0", Type: TypeText, Open: true})
	if back.Status != StatusClosed {
		t.Fatal("coming back to a validated slide must not reopen it")
	}
	p, _ := c.CreateQuestion(NewQuestion{Key: "later", Type: TypeText})
	opened, _ := c.CreateQuestion(NewQuestion{Key: "later", Type: TypeText, Open: true})
	if p.Status != StatusProposed || opened.Status != StatusOpen {
		t.Fatal("a prepared keyed question opens on demand")
	}
	if _, err := c.CreateQuestion(NewQuestion{Key: strings.Repeat("k", MaxKeyLen+1), Type: TypeText}); !errors.Is(err, ErrTooLong) {
		t.Fatal(err)
	}
}

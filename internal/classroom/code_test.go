package classroom

import (
	"errors"
	"strings"
	"testing"
)

func TestSplitTemplate(t *testing.T) {
	tests := []struct {
		name                   string
		tpl                    string
		before, starter, after string
		err                    error
	}{
		{name: "no marker: all editable", tpl: "int x = 1;\n", starter: "int x = 1;"},
		{name: "one marker keeps its indentation",
			tpl:    "class A {\n    @@\n}",
			before: "class A {\n", starter: "    ", after: "\n}"},
		{name: "two markers enclose starter code",
			tpl:    "class A {\n  @@\n  int f() {\n    return 0;\n  }\n  @@\n}\n",
			before: "class A {\n", starter: "  int f() {\n    return 0;\n  }", after: "\n}"},
		{name: "marker first", tpl: "@@\n}", after: "\n}"},
		{name: "marker last", tpl: "class A {\n@@", before: "class A {\n"},
		{name: "windows line endings", tpl: "a\r\n@@\r\nb", before: "a\n", after: "\nb"},
		{name: "three markers", tpl: "@@\n@@\n@@", err: ErrBadTemplate},
		{name: "@@ inside a line is not a marker", tpl: "// @@ ici", starter: "// @@ ici"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := newCodeSpec(&CodeInput{Template: tt.tpl})
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if err != nil {
				return
			}
			if spec.Before != tt.before || spec.Starter != tt.starter || spec.After != tt.after {
				t.Fatalf("got before=%q starter=%q after=%q", spec.Before, spec.Starter, spec.After)
			}
			if spec.Language != "java" {
				t.Fatalf("default language = %q", spec.Language)
			}
		})
	}
}

func TestCodeSpecValidation(t *testing.T) {
	if _, err := newCodeSpec(&CodeInput{Language: "cobol"}); !errors.Is(err, ErrBadLanguage) {
		t.Fatalf("unknown language: %v", err)
	}
	if _, err := newCodeSpec(&CodeInput{Language: "Java", Context: strings.Repeat("x", MaxContextLen+1)}); !errors.Is(err, ErrTooLong) {
		t.Fatalf("huge context: %v", err)
	}
	if _, err := newCodeSpec(&CodeInput{Template: "\xff"}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	spec, err := newCodeSpec(&CodeInput{Language: " JAVA ", Context: "\n\nclass A {}\n\n"})
	if err != nil || spec.Language != "java" || spec.Context != "class A {}" {
		t.Fatalf("spec = %+v, %v", spec, err)
	}
}

func TestCodeKeyIgnoresLayoutOnly(t *testing.T) {
	same := [][2]string{
		{"return a+b;", "return  a + b ;"},
		{"if (x) {\n    y();\n}", "if(x){ y(); }"},
		{"int x;", "int\n\tx;"},
	}
	for _, p := range same {
		if codeKey(p[0]) != codeKey(p[1]) {
			t.Errorf("%q and %q should match: %q vs %q", p[0], p[1], codeKey(p[0]), codeKey(p[1]))
		}
	}
	different := [][2]string{
		{"int x;", "intx;"},             // words must stay apart
		{`s = "a b";`, `s = "ab";`},     // spaces inside strings matter
		{`c = ' ';`, `c = '';`},         // and inside chars
		{`s = "a\" b";`, `s = "a\"b";`}, // escaped quote does not end the string
		{"Total t;", "total t;"},        // Java is case-sensitive
	}
	for _, p := range different {
		if codeKey(p[0]) == codeKey(p[1]) {
			t.Errorf("%q and %q should differ", p[0], p[1])
		}
	}
}

func TestNormalizeCode(t *testing.T) {
	got := normalizeCode("\n  \n    return 1;\r\n  }\n\n  ")
	if got != "    return 1;\n  }" {
		t.Fatalf("got %q", got)
	}
}

func TestCodeQuestionLifecycle(t *testing.T) {
	c, _, _ := newTest(t)
	sid := mustJoin(t, c, "alice")
	bob := mustJoin(t, c, "bob")
	q, err := c.CreateQuestion(NewQuestion{Title: "Écris total()", Type: TypeCode, Open: true,
		Code: &CodeInput{Context: "class Ligne { double prix() { return 1; } }", Template: "class Commande {\n  @@\n}"}})
	if err != nil {
		t.Fatal(err)
	}
	if q.Code == nil || q.Code.Before != "class Commande {\n" || q.Code.Starter != "  " {
		t.Fatalf("code spec = %+v", q.Code)
	}

	v, err := c.StudentView(sid)
	if err != nil || v.Question.Code == nil || v.Question.Code.Context == "" {
		t.Fatalf("students need the code around their answer: %+v %v", v.Question, err)
	}

	long := strings.Repeat("x", MaxAnswerLen+10) // longer than a text answer, fine for code
	if err := c.UpdateDraft(sid, q.ID, long); err != nil {
		t.Fatalf("long code draft: %v", err)
	}
	if err := c.UpdateDraft(sid, q.ID, strings.Repeat("x", MaxCodeLen+1)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("code over the limit: %v", err)
	}

	if err := c.Submit(sid, q.ID, "\n  double total() {\n    return 0;\n  }\n\n"); err != nil {
		t.Fatal(err)
	}
	if err := c.Submit(bob, q.ID, "double total(){ return 0; }"); err != nil {
		t.Fatal(err)
	}
	answers, _ := c.Answers(q.ID)
	if answers[0].Value != "  double total() {\n    return 0;\n  }" {
		t.Fatalf("indentation must be kept: %q", answers[0].Value)
	}
	st, _ := c.Stats(q.ID)
	if len(st.TextGroups) != 1 || st.TextGroups[0].Count != 2 || st.Words != nil {
		t.Fatalf("same code with another layout must be grouped: %+v", st.TextGroups)
	}

	if err := c.SetCorrect(q.ID, []string{"double total() {return 0;}"}); err != nil {
		t.Fatal(err)
	}
	st, _ = c.Stats(q.ID)
	if st.CorrectPct == nil || *st.CorrectPct != 100 {
		t.Fatalf("correct code compared without layout: %+v", st.CorrectPct)
	}
}

func TestTextAnswersKeepTheirLimit(t *testing.T) {
	c, _, _ := newTest(t)
	sid := mustJoin(t, c, "alice")
	q := mustOpen(t, c, TypeText)
	if err := c.UpdateDraft(sid, q.ID, strings.Repeat("x", MaxAnswerLen+1)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("text draft over the limit: %v", err)
	}
	if err := c.Submit(sid, q.ID, strings.Repeat("x", MaxAnswerLen+1)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("text answer over the limit: %v", err)
	}
	if q.Code != nil {
		t.Fatal("text questions have no code spec")
	}
}

func TestTeacherViewTruncatesCodeLater(t *testing.T) {
	c, _, _ := newTest(t)
	sid := mustJoin(t, c, "alice")
	q, err := c.CreateQuestion(NewQuestion{Type: TypeCode, Open: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateDraft(sid, q.ID, strings.Repeat("y", teacherCodeLen+50)); err != nil {
		t.Fatal(err)
	}
	v := c.TeacherView(q.ID)
	if n := len([]rune(v.View.Answers[0].Draft)); n != teacherCodeLen {
		t.Fatalf("code draft shown with %d runes, want %d", n, teacherCodeLen)
	}
}

func TestImportsMoveToTheTop(t *testing.T) {
	spec, err := newCodeSpec(&CodeInput{
		Context:  "class A {}\nimport java.util.List;",
		Template: "import java.util.*;\nimport java.util.List;\n\nclass B extends A {\n  @@\n}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Context != "import java.util.List;\nimport java.util.*;\n\nclass A {}" {
		t.Fatalf("context = %q", spec.Context)
	}
	if spec.Before != "class B extends A {\n" {
		t.Fatalf("before = %q", spec.Before)
	}
	// without provided code the template is left as written
	spec, _ = newCodeSpec(&CodeInput{Template: "import java.util.*;\nclass B {\n@@\n}"})
	if spec.Before != "import java.util.*;\nclass B {\n" {
		t.Fatalf("before = %q", spec.Before)
	}
}

func TestEditorAssistedInsertIsNoBurst(t *testing.T) {
	c, _, _ := newTest(t)
	sid := mustJoin(t, c, "alice")
	q, err := c.CreateQuestion(NewQuestion{Type: TypeCode, Open: true})
	if err != nil {
		t.Fatal(err)
	}
	indented := strings.Repeat("    x\n", 10) // a 10-line selection indented with Tab
	if err := c.UpdateDraftAssisted(sid, q.ID, indented, 40); err != nil {
		t.Fatal(err)
	}
	if a, _ := c.Answers(q.ID); a[0].Bursts != 0 {
		t.Fatalf("editor indentation flagged as a burst")
	}
	if err := c.UpdateDraftAssisted(sid, q.ID, indented+strings.Repeat("y", 60), 0); err != nil {
		t.Fatal(err)
	}
	if a, _ := c.Answers(q.ID); a[0].Bursts != 1 {
		t.Fatalf("a real sudden insertion must still be flagged")
	}

	// a text question ignores the claim
	tq := mustOpen(t, c, TypeText)
	if err := c.UpdateDraftAssisted(sid, tq.ID, strings.Repeat("z", 60), 60); err != nil {
		t.Fatal(err)
	}
	if a, _ := c.Answers(tq.ID); a[0].Bursts != 1 {
		t.Fatalf("assisted characters only exist in the code editor")
	}
}

func TestStarterCodeIsNoBurst(t *testing.T) {
	c, _, _ := newTest(t)
	sid := mustJoin(t, c, "alice")
	starter := "    double t = 0; // à compléter avec la boucle"
	q, err := c.CreateQuestion(NewQuestion{Type: TypeCode, Open: true,
		Code: &CodeInput{Template: "class A {\n  double f() {\n@@\n" + starter + "\n@@\n  }\n}"}})
	if err != nil || q.Code.Starter != starter {
		t.Fatalf("starter = %q, %v", q.Code.Starter, err)
	}
	if err := c.UpdateDraft(sid, q.ID, starter+"x"); err != nil {
		t.Fatal(err)
	}
	if a, _ := c.Answers(q.ID); a[0].Bursts != 0 {
		t.Fatal("the starter code shown by the editor is not a sudden insertion")
	}
}

func TestCodeKeyIgnoresComments(t *testing.T) {
	if codeKey("return t; // c'est fini") != codeKey("return t;") {
		t.Fatal("a comment (with an apostrophe) must not change the key")
	}
	if codeKey("a /* x */ b") != codeKey("a b") || codeKey(`s = "// pas un commentaire";`) == codeKey("s = ;") {
		t.Fatal("block comments ignored, strings kept")
	}
}

func TestHoistImportsEdgeCases(t *testing.T) {
	spec, err := newCodeSpec(&CodeInput{
		Context:  "package cours;\nimport java.util.List; // listes\n/*\nimport pas.Ici;\n*/\nclass A {}",
		Template: "import static java.lang.Math.max;\nclass B {\n@@\n}",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "package cours;\nimport java.util.List;\nimport static java.lang.Math.max;\n\n/*\nimport pas.Ici;\n*/\nclass A {}"
	if spec.Context != want {
		t.Fatalf("context = %q", spec.Context)
	}
}

func TestSlideEditRefreshesProposedQuestion(t *testing.T) {
	c, _, _ := newTest(t)
	q, _ := c.CreateQuestion(NewQuestion{Key: "s1", Type: TypeCode, Code: &CodeInput{Context: "class A {}"}})
	q2, err := c.CreateQuestion(NewQuestion{Key: "s1", Type: TypeCode, Code: &CodeInput{Context: "class B {}"}})
	if err != nil || q2.ID != q.ID || q2.Code.Context != "class B {}" {
		t.Fatalf("proposed question keeps stale code: %+v %v", q2.Code, err)
	}
}

func TestTeacherViewSubmittedCodeHasNoDuplicateDraft(t *testing.T) {
	c, _, _ := newTest(t)
	sid := mustJoin(t, c, "alice")
	q, _ := c.CreateQuestion(NewQuestion{Type: TypeCode, Open: true})
	long := strings.Repeat("z", teacherCodeLen+10)
	if err := c.Submit(sid, q.ID, long); err != nil {
		t.Fatal(err)
	}
	v := c.TeacherView(q.ID)
	if v.View.Answers[0].Draft != "" || len([]rune(v.View.Stats.TextGroups[0].Label)) != teacherCodeLen {
		t.Fatalf("frame too big: draft %d, label %d", len(v.View.Answers[0].Draft), len(v.View.Stats.TextGroups[0].Label))
	}
}

package classroom

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits of code questions. An answer stays well under the student WebSocket
// read limit even once JSON-escaped.
const (
	MaxCodeLen     = 10000
	MaxContextLen  = 32 << 10 // provided code: classes explained earlier, template
	teacherCodeLen = 2000     // drafts, answers and group labels pushed to the dashboard at 4 Hz
)

// AnswerMarker is the line of a template that marks where the student writes.
// One marker line is an empty answer zone; two marker lines enclose starter code.
const AnswerMarker = "@@"

// Languages supported by the code editor.
var Languages = []string{"java"}

// Errors of code questions.
var (
	ErrBadLanguage = errors.New("langage non pris en charge (java)")
	ErrBadTemplate = errors.New("modèle invalide : une ligne @@ (zone vide) ou deux (code de départ entre les deux)")
)

// CodeInput is how a code question is described by the teacher or a slide.
type CodeInput struct {
	Language string
	// Context is read-only code shown above the template, typically the classes
	// explained on earlier slides.
	Context string
	// Template is the code around the answer, with AnswerMarker lines. Without
	// marker the whole template is starter code the student edits.
	Template string
}

// CodeSpec is what the editor shows: Context, then Before, the editable
// answer (initially Starter), then After. Only the answer is sent back.
type CodeSpec struct {
	Language string `json:"language"`
	Context  string `json:"context,omitempty"`
	Before   string `json:"before,omitempty"`
	Starter  string `json:"starter,omitempty"`
	After    string `json:"after,omitempty"`
}

func newCodeSpec(in *CodeInput) (*CodeSpec, error) {
	if in == nil {
		in = &CodeInput{}
	}
	lang := strings.ToLower(strings.TrimSpace(in.Language))
	if lang == "" {
		lang = "java"
	}
	if !slices.Contains(Languages, lang) {
		return nil, ErrBadLanguage
	}
	ctx := strings.Trim(strings.ReplaceAll(in.Context, "\r\n", "\n"), "\n")
	tpl := strings.ReplaceAll(in.Template, "\r\n", "\n")
	if utf8.RuneCountInString(ctx)+utf8.RuneCountInString(tpl) > MaxContextLen {
		return nil, ErrTooLong
	}
	if !utf8.ValidString(ctx) || !utf8.ValidString(tpl) {
		return nil, ErrBadTemplate
	}
	spec := &CodeSpec{Language: lang, Context: ctx}
	before, starter, after, err := splitTemplate(tpl)
	if err != nil {
		return nil, err
	}
	spec.Before, spec.Starter, spec.After = before, starter, after
	if spec.Context != "" {
		hoistImports(spec)
	}
	return spec, nil
}

// hoistImports moves the import lines of the provided code and of the
// template to the top: snippets come first in the editor, and Java wants
// imports before any class.
func hoistImports(spec *CodeSpec) {
	var imports []string
	seen := map[string]bool{}
	var pkg string
	take := func(code string) string {
		var kept []string
		inComment := false
		for _, l := range strings.Split(code, "\n") {
			if inComment {
				inComment = !strings.Contains(l, "*/")
				kept = append(kept, l)
				continue
			}
			t := strings.TrimSpace(l)
			if m := importLine.FindStringSubmatch(t); m != nil {
				if m[1] == "package" {
					if pkg == "" {
						pkg = m[0]
					}
				} else if !seen[m[0]] {
					seen[m[0]] = true
					imports = append(imports, m[0])
				}
				continue
			}
			if i := strings.LastIndex(l, "/*"); i >= 0 && !strings.Contains(l[i:], "*/") {
				inComment = true
			}
			kept = append(kept, l)
		}
		return strings.Join(kept, "\n")
	}
	ctx := strings.Trim(take(spec.Context), "\n")
	before := strings.TrimLeft(take(spec.Before), "\n")
	if len(imports) == 0 && pkg == "" {
		return
	}
	if pkg != "" {
		imports = append([]string{pkg}, imports...)
	}
	spec.Context = strings.Join(imports, "\n") + "\n\n" + ctx
	spec.Before = before
}

// splitTemplate cuts a template around its marker lines. Before ends with a
// newline and After starts with one, so the answer always has lines of its own.
func splitTemplate(tpl string) (before, starter, after string, err error) {
	tpl = strings.Trim(tpl, "\n")
	lines := strings.Split(tpl, "\n")
	var marks []int
	for i, l := range lines {
		if strings.TrimSpace(l) == AnswerMarker {
			marks = append(marks, i)
		}
	}
	join := func(ls []string) string { return strings.Join(ls, "\n") }
	switch len(marks) {
	case 0:
		return "", tpl, "", nil
	case 1:
		m := marks[0]
		before, after = join(lines[:m]), join(lines[m+1:])
		// the cursor starts at the marker's indentation
		starter = lines[m][:len(lines[m])-len(strings.TrimLeftFunc(lines[m], unicode.IsSpace))]
	case 2:
		a, b := marks[0], marks[1]
		before, starter, after = join(lines[:a]), join(lines[a+1:b]), join(lines[b+1:])
	default:
		return "", "", "", ErrBadTemplate
	}
	if before != "" {
		before += "\n"
	}
	if after != "" {
		after = "\n" + after
	}
	return before, starter, after, nil
}

// importLine is a package or import declaration, without a trailing comment.
var importLine = regexp.MustCompile(`^(package|import)\s+(?:static\s+)?[\w.]+(?:\.\*)?\s*;`)

func copyCode(c *CodeSpec) *CodeSpec {
	if c == nil {
		return nil
	}
	out := *c
	return &out
}

// normalizeCode keeps the code as typed, minus blank lines around it.
func normalizeCode(raw string) string {
	v := strings.TrimRightFunc(strings.ReplaceAll(raw, "\r\n", "\n"), unicode.IsSpace)
	for {
		i := strings.IndexByte(v, '\n')
		if i < 0 || strings.TrimSpace(v[:i]) != "" {
			return v
		}
		v = v[i+1:]
	}
}

// codeKey makes answers that only differ by layout compare equal: spacing,
// indentation and line breaks are ignored, except inside string and char
// literals. Identifiers stay case-sensitive, as in Java.
func codeKey(s string) string {
	var b strings.Builder
	var quote rune
	space := false
	prev := rune(0)
	word := func(r rune) bool { return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	escaped := false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if quote == 0 && r == '/' && i+1 < len(rs) && (rs[i+1] == '/' || rs[i+1] == '*') {
			i = skipComment(rs, i) // comments do not change the code
			space = true
			continue
		}
		if quote != 0 {
			b.WriteRune(r)
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == quote:
				quote = 0
			}
			prev = r
			continue
		}
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		// a space only matters between two words: "int x" vs "intx"
		if space && word(prev) && word(r) {
			b.WriteByte(' ')
		}
		space = false
		if r == '"' || r == '\'' {
			quote = r
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

// skipComment returns the index of the last rune of the comment at i.
func skipComment(rs []rune, i int) int {
	if rs[i+1] == '/' {
		for i < len(rs) && rs[i] != '\n' {
			i++
		}
		return i
	}
	for i += 2; i+1 < len(rs); i++ {
		if rs[i] == '*' && rs[i+1] == '/' {
			return i + 1
		}
	}
	return len(rs)
}

// answerKey is how answers to a question are compared and grouped.
func answerKey(q Question, v string) string {
	if q.Type == TypeCode {
		return codeKey(v)
	}
	return normalizeText(v)
}

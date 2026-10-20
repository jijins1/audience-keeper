package classroom

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Bucket is one bar of a distribution.
type Bucket struct {
	Label      string   `json:"label"`
	Count      int      `json:"count"`
	Correct    bool     `json:"correct,omitempty"`
	StudentIDs []string `json:"studentIds,omitempty"`
}

// NumericStats summarises number answers.
type NumericStats struct {
	Min       float64  `json:"min"`
	Max       float64  `json:"max"`
	Mean      float64  `json:"mean"`
	Median    float64  `json:"median"`
	Histogram []Bucket `json:"histogram"`
}

// WordCount is one entry of the word cloud.
type WordCount struct {
	Word  string `json:"word"`
	Count int    `json:"count"`
}

// Stats is computed from the submitted answers of a question.
type Stats struct {
	QuestionID       string        `json:"questionId"`
	Participants     int           `json:"participants"`
	Answered         int           `json:"answered"`
	Typing           int           `json:"typing"`
	ParticipationPct float64       `json:"participationPct"`
	MedianResponseMs int64         `json:"medianResponseMs"`
	AvgResponseMs    int64         `json:"avgResponseMs"`
	WithPaste        int           `json:"withPaste"`
	WithBurst        int           `json:"withBurst"`
	WithBlur         int           `json:"withBlur"`
	Distribution     []Bucket      `json:"distribution,omitempty"`
	Numeric          *NumericStats `json:"numeric,omitempty"`
	TextGroups       []Bucket      `json:"textGroups,omitempty"`
	Words            []WordCount   `json:"words,omitempty"`
	Correct          int           `json:"correct"`
	Partial          int           `json:"partial"`
	Wrong            int           `json:"wrong"`
	CorrectPct       *float64      `json:"correctPct,omitempty"`
}

const (
	maxTextGroups = 20
	maxWords      = 30
	histBuckets   = 8
)

// Stats computes statistics for a question.
func (c *Classroom) Stats(qid string) (Stats, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	q, ok := c.byQID[qid]
	if !ok {
		return Stats{}, ErrUnknownQuestion
	}
	return computeStats(*q, c.answersLocked(qid), len(c.studentList)), nil
}

func computeStats(q Question, answers []Answer, participants int) Stats {
	s := Stats{QuestionID: q.ID, Participants: participants}
	var submitted []Answer
	var times []int64
	for _, a := range answers {
		if a.Pastes > 0 {
			s.WithPaste++
		}
		if a.Bursts > 0 {
			s.WithBurst++
		}
		if a.Blurs > 0 {
			s.WithBlur++
		}
		if !a.Submitted {
			if strings.TrimSpace(a.Draft) != "" {
				s.Typing++
			}
			continue
		}
		submitted = append(submitted, a)
		if !a.AutoSubmitted && !q.OpenedAt.IsZero() && a.SubmittedAt.After(q.OpenedAt) {
			times = append(times, a.SubmittedAt.Sub(q.OpenedAt).Milliseconds())
		}
	}
	s.Answered = len(submitted)
	if participants > 0 {
		s.ParticipationPct = round1(100 * float64(s.Answered) / float64(participants))
	}
	s.AvgResponseMs, s.MedianResponseMs = avgMedian(times)
	gradeStats(&s, q, submitted)

	switch q.Type {
	case TypeChoice, TypeYesNo:
		s.Distribution = choiceDistribution(q, submitted)
	case TypeNumber:
		s.Numeric = numericStats(submitted)
	case TypeText:
		s.TextGroups = textGroups(q, submitted)
		s.Words = topWords(submitted)
	case TypeCode:
		s.TextGroups = textGroups(q, submitted) // same code, whatever the layout
	}
	return s
}

// IsCorrect tells whether an answer counts as correct: teacher grade wins,
// otherwise it is compared to the accepted values.
func IsCorrect(q Question, a Answer) Grade {
	if a.TeacherGrade != GradeNone {
		return a.TeacherGrade
	}
	if len(q.Correct) == 0 || !a.Submitted {
		return GradeNone
	}
	for _, v := range q.Correct {
		if answerKey(q, v) == answerKey(q, a.Value) {
			return GradeCorrect
		}
	}
	return GradeWrong
}

func gradeStats(s *Stats, q Question, submitted []Answer) {
	graded := 0
	for _, a := range submitted {
		switch IsCorrect(q, a) {
		case GradeCorrect:
			s.Correct++
			graded++
		case GradePartial:
			s.Partial++
			graded++
		case GradeWrong:
			s.Wrong++
			graded++
		}
	}
	if graded > 0 {
		pct := round1(100 * float64(s.Correct) / float64(graded))
		s.CorrectPct = &pct
	}
}

func choiceDistribution(q Question, submitted []Answer) []Bucket {
	out := make([]Bucket, len(q.Choices))
	idx := map[string]int{}
	for i, ch := range q.Choices {
		out[i] = Bucket{Label: ch, Correct: contains(q.Correct, ch)}
		idx[ch] = i
	}
	for _, a := range submitted {
		if i, ok := idx[a.Value]; ok {
			out[i].Count++
			out[i].StudentIDs = append(out[i].StudentIDs, a.StudentID)
		}
	}
	return out
}

func numericStats(submitted []Answer) *NumericStats {
	vals := make([]float64, 0, len(submitted))
	for _, a := range submitted {
		if f, err := strconv.ParseFloat(a.Value, 64); err == nil {
			vals = append(vals, f)
		}
	}
	if len(vals) == 0 {
		return nil
	}
	sort.Float64s(vals)
	mean := 0.0 // incremental mean: no overflow even near MaxNumber
	for i, v := range vals {
		mean += (v - mean) / float64(i+1)
	}
	ns := &NumericStats{Min: vals[0], Max: vals[len(vals)-1], Mean: mean, Median: medianF(vals)}
	ns.Histogram = histogram(vals, ns.Min, ns.Max)
	return ns
}

func histogram(vals []float64, lo, hi float64) []Bucket {
	if lo == hi {
		return []Bucket{{Label: fmtNum(lo), Count: len(vals)}}
	}
	n := histBuckets
	if len(vals) < n {
		n = len(vals)
	}
	width := (hi - lo) / float64(n)
	out := make([]Bucket, n)
	for i := range out {
		a, b := lo+width*float64(i), lo+width*float64(i+1)
		out[i].Label = fmtNum(a) + "–" + fmtNum(b)
	}
	for _, v := range vals {
		i := int((v - lo) / width)
		i = max(0, min(i, n-1))
		out[i].Count++
	}
	return out
}

func textGroups(q Question, submitted []Answer) []Bucket {
	byKey := map[string]*Bucket{}
	var order []string
	for _, a := range submitted {
		k := answerKey(q, a.Value)
		b, ok := byKey[k]
		if !ok {
			b = &Bucket{Label: a.Value}
			for _, v := range q.Correct {
				if answerKey(q, v) == k {
					b.Correct = true
				}
			}
			byKey[k] = b
			order = append(order, k)
		}
		b.Count++
		b.StudentIDs = append(b.StudentIDs, a.StudentID)
	}
	out := make([]Bucket, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if len(out) > maxTextGroups {
		out = out[:maxTextGroups]
	}
	return out
}

func topWords(submitted []Answer) []WordCount {
	counts := map[string]int{}
	for _, a := range submitted {
		seen := map[string]bool{}
		for _, w := range strings.FieldsFunc(strings.ToLower(a.Value), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}) {
			if len([]rune(w)) < 3 || stopWords[w] || seen[w] {
				continue
			}
			seen[w] = true
			counts[w]++
		}
	}
	out := make([]WordCount, 0, len(counts))
	for w, n := range counts {
		out = append(out, WordCount{Word: w, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Word < out[j].Word
	})
	if len(out) > maxWords {
		out = out[:maxWords]
	}
	return out
}

// normalizeText makes near-identical answers compare equal.
func normalizeText(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	return strings.TrimRightFunc(s, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
}

func avgMedian(xs []int64) (int64, int64) {
	if len(xs) == 0 {
		return 0, 0
	}
	sorted := append([]int64(nil), xs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var sum int64
	for _, x := range sorted {
		sum += x
	}
	mid := len(sorted) / 2
	med := sorted[mid]
	if len(sorted)%2 == 0 {
		med = (sorted[mid-1] + sorted[mid]) / 2
	}
	return sum / int64(len(sorted)), med
}

func medianF(sorted []float64) float64 {
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

func fmtNum(f float64) string { return strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64) }

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

var stopWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`les des une est que qui pas pour par sur dans avec son ses aux mais donc car
		cette ces ont été être avoir fait faire elle ils elles nous vous leur leurs tout tous très plus moins
		comme quand alors aussi bien peut sont the and for are with that this from not you have`) {
		m[w] = true
	}
	return m
}()

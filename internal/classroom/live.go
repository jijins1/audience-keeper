package classroom

import (
	"strings"
)

// Limits for the slide mirrored to students.
const (
	MaxSlideHTML  = 256 << 10
	MaxSlideTitle = 200
	maxThemes     = 8
	maxFragment   = 1000
	MaxNotesHTML  = 64 << 10
	maxQKeyLen    = MaxKeyLen
)

// LiveSlide is the slide currently presented by the teacher, mirrored on
// student screens. Only this slide is ever sent: students cannot read ahead.
type LiveSlide struct {
	Deck     string   `json:"deck"` // presentation title
	HTML     string   `json:"html"` // the current <section>, stripped of notes and scripts by the deck plugin
	H        int      `json:"h"`    // reveal indices, informative
	V        int      `json:"v"`
	Fragment int      `json:"fragment"` // index of the last visible fragment, -1 for none
	Themes   []string `json:"themes"`   // stylesheets to load (local paths or allowed CDNs)
	Rev      int64    `json:"rev"`
}

// PresenterInfo is what only the teacher sees about the current slide: speaker
// notes, the next slide, progress. It is never part of a student view.
type PresenterInfo struct {
	Notes       string `json:"notes"`       // speaker notes (HTML, sanitised on display)
	NextHTML    string `json:"nextHtml"`    // the following slide, for the preview
	Number      int    `json:"number"`      // 1-based position of the current slide
	Total       int    `json:"total"`       // number of slides in the deck
	Fragments   int    `json:"fragments"`   // fragments on the current slide
	QuestionKey string `json:"questionKey"` // key of the slide's question, if it is one
}

// allowedStyleOrigins mirrors the student page CSP style-src.
var allowedStyleOrigins = []string{"https://cdn.jsdelivr.net/", "https://unpkg.com/", "https://cdnjs.cloudflare.com/", "https://fonts.googleapis.com/"}

func validTheme(href string) bool {
	if strings.HasPrefix(href, "/static/") || strings.HasPrefix(href, "/deck/") {
		return !strings.Contains(href, "..") && !strings.HasPrefix(href, "//")
	}
	for _, o := range allowedStyleOrigins {
		if strings.HasPrefix(href, o) {
			return true
		}
	}
	return false
}

// SetSlide publishes the teacher's current slide to every student, and the
// presenter details to the teacher only.
func (c *Classroom) SetSlide(s LiveSlide, p PresenterInfo) error {
	if len(s.HTML) > MaxSlideHTML || len(p.NextHTML) > MaxSlideHTML || len(p.Notes) > MaxNotesHTML || len(p.QuestionKey) > maxQKeyLen {
		return ErrTooLong
	}
	if p.Number < 0 || p.Total < 0 || p.Number > p.Total || p.Fragments < 0 || p.Fragments > maxFragment {
		return ErrBadEvent
	}
	if err := validText(s.Deck, MaxSlideTitle); err != nil {
		return err
	}
	if s.Fragment < -1 || s.Fragment > maxFragment || s.H < 0 || s.V < 0 {
		return ErrBadEvent
	}
	if len(s.Themes) > maxThemes {
		return ErrTooLong
	}
	themes := make([]string, 0, len(s.Themes))
	for _, t := range s.Themes {
		if !validTheme(t) {
			return ErrBadEvent
		}
		themes = append(themes, t)
	}
	return c.mutate(func() ([]string, error) {
		c.slideRev++
		c.slide = &LiveSlide{Deck: s.Deck, HTML: s.HTML, H: s.H, V: s.V, Fragment: s.Fragment, Themes: themes, Rev: c.slideRev}
		c.presenter = &p
		return everyone(), nil
	})
}

// ClearSlide stops mirroring the presentation.
func (c *Classroom) ClearSlide() {
	_ = c.mutate(func() ([]string, error) {
		c.slide = nil
		c.presenter = nil
		return everyone(), nil
	})
}

// Slide returns the mirrored slide, if any.
func (c *Classroom) Slide() (LiveSlide, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.slide == nil {
		return LiveSlide{}, false
	}
	return copySlide(c.slide), true
}

// Presenter returns the mirrored slide with its teacher-only details.
func (c *Classroom) Presenter() (LiveSlide, PresenterInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.slide == nil || c.presenter == nil {
		return LiveSlide{}, PresenterInfo{}, false
	}
	return copySlide(c.slide), *c.presenter, true
}

func copySlide(s *LiveSlide) LiveSlide {
	out := *s
	out.Themes = append([]string(nil), s.Themes...)
	return out
}

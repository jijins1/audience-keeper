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

// SetSlide publishes the teacher's current slide to every student.
func (c *Classroom) SetSlide(s LiveSlide) error {
	if len(s.HTML) > MaxSlideHTML {
		return ErrTooLong
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
		return everyone(), nil
	})
}

// ClearSlide stops mirroring the presentation.
func (c *Classroom) ClearSlide() {
	_ = c.mutate(func() ([]string, error) {
		c.slide = nil
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

func copySlide(s *LiveSlide) LiveSlide {
	out := *s
	out.Themes = append([]string(nil), s.Themes...)
	return out
}

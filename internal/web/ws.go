package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/time/rate"

	"audience-keeper/internal/classroom"
)

const (
	wsReadLimit    = 64 << 10
	teacherReadLim = 640 << 10 // a mirrored slide and the next one, up to 256 KiB each, plus notes
	wsWriteTimeout = 10 * time.Second
	wsPingEvery    = 25 * time.Second
	helloTimeout   = 10 * time.Second
	maxBadMessages = 20
	maxStudentTabs = 5
	sessionRecheck = 5 * time.Second
)

// inbound is the union of every client -> server message.
type inbound struct {
	T string `json:"t"`

	// student
	Token string `json:"token,omitempty"`
	Q     string `json:"q,omitempty"`
	Text  string `json:"text,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Chars int    `json:"chars,omitempty"`
	Ms    int64  `json:"ms,omitempty"`
	Value string `json:"value,omitempty"`
	// characters the code editor inserted itself (completion, indentation, undo)
	Assisted int `json:"assisted,omitempty"`

	// teacher
	Title   string   `json:"title,omitempty"`
	Type    string   `json:"type,omitempty"`
	Choices int      `json:"choices,omitempty"`
	Open    bool     `json:"open,omitempty"`
	Values  []string `json:"values,omitempty"`
	S       string   `json:"s,omitempty"`
	Grade   string   `json:"grade,omitempty"`
	ID      string   `json:"id,omitempty"`
	Mode    string   `json:"mode,omitempty"`
	Key     string   `json:"key,omitempty"`
	Code    *codeIn  `json:"code,omitempty"`

	// deck mirroring
	Deck     string   `json:"deck,omitempty"`
	HTML     string   `json:"html,omitempty"`
	H        int      `json:"h,omitempty"`
	V        int      `json:"v,omitempty"`
	Fragment int      `json:"fragment,omitempty"`
	Themes   []string `json:"themes,omitempty"`

	// presenter details (teacher only) and remote control
	Notes       string `json:"notes,omitempty"`
	NextHTML    string `json:"nextHtml,omitempty"`
	Number      int    `json:"number,omitempty"`
	Total       int    `json:"total,omitempty"`
	Fragments   int    `json:"fragments,omitempty"`
	QuestionKey string `json:"questionKey,omitempty"`
	Action      string `json:"action,omitempty"`
}

// codeIn describes a code question (language, provided code, template).
type codeIn struct {
	Language string `json:"language"`
	Context  string `json:"context"`
	Template string `json:"template"`
}

func (c *codeIn) input() *classroom.CodeInput {
	if c == nil {
		return nil
	}
	return &classroom.CodeInput{Language: c.Language, Context: c.Context, Template: c.Template}
}

func errMsg(err error) []byte {
	return mustJSON(envelope{T: "error", D: map[string]string{"msg": err.Error()}})
}

// pump runs the write loop and keepalive for a connection; it returns when ctx ends.
func pump(ctx context.Context, conn *websocket.Conn, c *client) {
	ping := time.NewTicker(wsPingEvery)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-c.send:
			if b == nil {
				_ = conn.Close(websocket.StatusNormalClosure, "séance terminée")
				c.cancel()
				return
			}
			wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := conn.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}

func readJSON(ctx context.Context, conn *websocket.Conn) (inbound, error) {
	var m inbound
	_, b, err := conn.Read(ctx)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, errBadMessage
	}
	return m, nil
}

var errBadMessage = errors.New("message invalide")

func (s *Server) accept(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.cfg.AllowedOrigins})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(wsReadLimit)
	return conn, nil
}

func (s *Server) handleStudentWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.accept(w, r)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()

	hctx, hcancel := context.WithTimeout(r.Context(), helloTimeout)
	hello, err := readJSON(hctx, conn)
	hcancel()
	if err != nil || hello.T != "hello" {
		_ = conn.Close(websocket.StatusPolicyViolation, "hello attendu")
		return
	}
	sid, ok := s.room.Authenticate(hello.Token)
	if !ok {
		_ = conn.Write(r.Context(), websocket.MessageText, mustJSON(envelope{T: "ended"}))
		_ = conn.Close(websocket.StatusPolicyViolation, "session inconnue")
		return
	}

	if s.hub.studentConns(sid) >= maxStudentTabs {
		_ = conn.Close(websocket.StatusPolicyViolation, "trop d'onglets ouverts")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := &client{studentID: sid, send: make(chan []byte, sendBuffer), cancel: cancel}
	if err := s.room.Connect(sid); err != nil {
		return
	}
	defer s.room.Disconnect(sid)
	go pump(ctx, conn, c)
	s.hub.addStudent(c)
	defer s.hub.removeStudent(c)

	lim := rate.NewLimiter(40, 80)
	bad := 0
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if !lim.Allow() { // checked before parsing: floods cost nothing
			continue
		}
		var m inbound
		if err := json.Unmarshal(b, &m); err != nil {
			err = errBadMessage
			bad++
			c.trySend(errMsg(err))
		} else if err := s.studentMessage(sid, m); err != nil {
			if errors.Is(err, errBadMessage) {
				bad++
			}
			c.trySend(errMsg(err))
		}
		if bad >= maxBadMessages {
			_ = conn.Close(websocket.StatusPolicyViolation, "trop de messages invalides")
			return
		}
	}
}

func (s *Server) studentMessage(sid string, m inbound) error {
	switch m.T {
	case "draft":
		err := s.room.UpdateDraftAssisted(sid, m.Q, m.Text, m.Assisted)
		if errors.Is(err, classroom.ErrNotOpen) || errors.Is(err, classroom.ErrAlreadyAnswered) {
			return nil // late keystrokes after validation are expected
		}
		return err
	case "event":
		err := s.room.RecordEvent(sid, m.Q, classroom.ClientEvent{Kind: classroom.EventKind(m.Kind), Chars: m.Chars, DurationMs: m.Ms, Text: m.Text})
		if errors.Is(err, classroom.ErrNotOpen) || errors.Is(err, classroom.ErrUnknownQuestion) {
			return nil // signals outside a question are not tracked
		}
		return err
	case "submit":
		return s.room.Submit(sid, m.Q, m.Value)
	default:
		return errBadMessage
	}
}

func (s *Server) handleTeacherWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.accept(w, r)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	conn.SetReadLimit(teacherReadLim)
	c := &client{send: make(chan []byte, sendBuffer), cancel: cancel}
	go pump(ctx, conn, c)
	go s.watchSession(ctx, sessionID(r), cancel)
	s.hub.addTeacher(c)
	defer s.hub.removeTeacher(c)

	for {
		m, err := readJSON(ctx, conn)
		if errors.Is(err, errBadMessage) {
			c.trySend(errMsg(err))
			continue
		}
		if err != nil {
			return
		}
		switch m.T {
		case "slide":
			c.mirroring.Store(true)
		case "view":
			c.setView(m.Q)
			s.hub.pushTeacher(c)
			continue
		case "presenter":
			// a presenter page (laptop or phone) subscribes to teacher-only slide details
			c.presenting.Store(true)
			s.hub.pushPresenter(c)
			continue
		case "nav":
			if err := s.hub.relayNav(m.Action); err != nil {
				c.trySend(errMsg(err))
			}
			continue
		}
		if err := s.teacherMessage(m); err != nil {
			c.trySend(errMsg(err))
			continue
		}
		switch m.T {
		case "slide", "slide-off", "reset":
			s.hub.broadcastPresenter()
		}
	}
}

// watchSession closes a teacher socket once its session is logged out or expires.
func (s *Server) watchSession(ctx context.Context, id string, cancel context.CancelFunc) {
	t := time.NewTicker(sessionRecheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !s.auth.validID(id) {
				cancel()
				return
			}
		}
	}
}

func (s *Server) teacherMessage(m inbound) error {
	r := s.room
	switch m.T {
	case "create":
		_, err := r.CreateQuestion(classroom.NewQuestion{Title: m.Title, Type: classroom.QuestionType(m.Type), NumChoices: m.Choices, Open: m.Open, Key: m.Key, Code: m.Code.input()})
		return err
	case "open":
		return r.OpenQuestion(m.Q)
	case "close":
		return r.CloseQuestion(m.Q)
	case "delete":
		return r.DeleteQuestion(m.Q)
	case "correct":
		return r.SetCorrect(m.Q, m.Values)
	case "grade":
		return r.Grade(m.Q, m.S, classroom.Grade(m.Grade), true, "")
	case "message":
		_, err := r.SendMessage(m.S, m.Text, false)
		return err
	case "approve":
		return r.ApproveSuggestion(m.ID, m.Text)
	case "dismiss":
		return r.DismissSuggestion(m.ID)
	case "aimode":
		return r.SetAIMode(classroom.AIMode(m.Mode))
	case "slide":
		return r.SetSlide(
			classroom.LiveSlide{Deck: m.Deck, HTML: m.HTML, H: m.H, V: m.V, Fragment: m.Fragment, Themes: m.Themes},
			classroom.PresenterInfo{Notes: m.Notes, NextHTML: m.NextHTML, Number: m.Number, Total: m.Total, Fragments: m.Fragments, QuestionKey: m.QuestionKey})
	case "slide-off":
		r.ClearSlide()
		return nil
	case "reset":
		r.Reset()
		return nil
	default:
		return errBadMessage
	}
}

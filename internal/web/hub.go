package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"audience-keeper/internal/classroom"
)

const (
	teacherFlushEvery = 250 * time.Millisecond
	rulesEvery        = 5 * time.Second
	sendBuffer        = 64
)

// closeAfterFlush, queued after a message, closes the connection once that
// message has been written (e.g. "ended" after a session reset).
var closeAfterFlush = []byte(nil)

// envelope is the shape of every WebSocket message.
type envelope struct {
	T string `json:"t"`
	D any    `json:"d,omitempty"`
}

// client is one WebSocket connection.
type client struct {
	studentID  string // empty for the teacher
	send       chan []byte
	cancel     context.CancelFunc
	mu         sync.Mutex
	viewQID    string
	mirroring  atomic.Bool // a deck that publishes slides to students
	presenting atomic.Bool // a presenter page (notes, remote control)
}

func (c *client) setView(q string) {
	c.mu.Lock()
	c.viewQID = q
	c.mu.Unlock()
}

func (c *client) view() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.viewQID
}

// trySend queues a message; a full queue means a stuck client.
func (c *client) trySend(b []byte) bool {
	select {
	case c.send <- b:
		return true
	default:
		return false
	}
}

// Hub fans classroom changes out to connected pages. It implements
// classroom.Notifier: teacher views are coalesced and flushed periodically,
// student views are pushed immediately (they change rarely).
type Hub struct {
	room     *classroom.Classroom
	log      *slog.Logger
	mu       sync.Mutex
	teachers map[*client]struct{}
	students map[string]map[*client]struct{}
	dirty    atomic.Bool
	// mirrorGrace is how long the slide stays on student screens after the
	// last mirroring deck disconnects (long enough for a page reload).
	mirrorGrace time.Duration
}

// NewHub wires a hub to a classroom.
func NewHub(room *classroom.Classroom, log *slog.Logger) *Hub {
	h := &Hub{room: room, log: log, teachers: map[*client]struct{}{}, students: map[string]map[*client]struct{}{}, mirrorGrace: 10 * time.Second}
	room.SetNotifier(h)
	return h
}

// TeacherDirty implements classroom.Notifier.
func (h *Hub) TeacherDirty() { h.dirty.Store(true) }

// StudentsDirty implements classroom.Notifier.
func (h *Hub) StudentsDirty(ids ...string) {
	h.mu.Lock()
	targets := map[string][]*client{}
	if len(ids) == 0 {
		for sid, set := range h.students {
			targets[sid] = keys(set)
		}
	} else {
		for _, sid := range ids {
			targets[sid] = keys(h.students[sid])
		}
	}
	h.mu.Unlock()
	for sid, conns := range targets {
		if len(conns) > 0 {
			h.pushStudent(sid, conns)
		}
	}
}

func (h *Hub) pushStudent(sid string, conns []*client) {
	v, err := h.room.StudentView(sid)
	var msg []byte
	if err != nil {
		msg = mustJSON(envelope{T: "ended"})
	} else {
		msg = mustJSON(envelope{T: "state", D: v})
	}
	for _, c := range conns {
		if !c.trySend(msg) {
			c.cancel()
			continue
		}
		if err != nil && !c.trySend(closeAfterFlush) {
			c.cancel()
		}
	}
}

func (h *Hub) addStudent(c *client) {
	h.mu.Lock()
	if h.students[c.studentID] == nil {
		h.students[c.studentID] = map[*client]struct{}{}
	}
	h.students[c.studentID][c] = struct{}{}
	h.mu.Unlock()
	h.pushStudent(c.studentID, []*client{c})
}

func (h *Hub) removeStudent(c *client) {
	h.mu.Lock()
	delete(h.students[c.studentID], c)
	if len(h.students[c.studentID]) == 0 {
		delete(h.students, c.studentID)
	}
	h.mu.Unlock()
}

func (h *Hub) addTeacher(c *client) {
	h.mu.Lock()
	h.teachers[c] = struct{}{}
	h.mu.Unlock()
	h.pushTeacher(c)
}

func (h *Hub) removeTeacher(c *client) {
	h.mu.Lock()
	delete(h.teachers, c)
	h.mu.Unlock()
	if c.mirroring.Load() {
		time.AfterFunc(h.mirrorGrace, func() {
			if !h.anyMirroring() {
				h.room.ClearSlide()
				h.broadcastPresenter()
			}
		})
	}
}

// presenterFrame is sent to presenter pages: the live slide with the
// teacher-only details (notes, next slide), or nothing when no deck is live.
type presenterFrame struct {
	Live  bool                     `json:"live"`
	Slide *classroom.LiveSlide     `json:"slide,omitempty"`
	Info  *classroom.PresenterInfo `json:"info,omitempty"`
}

func (h *Hub) presenterMessage() ([]byte, bool) {
	f := presenterFrame{}
	if s, p, ok := h.room.Presenter(); ok {
		f = presenterFrame{Live: true, Slide: &s, Info: &p}
	}
	return h.encode(envelope{T: "presenter", D: f})
}

func (h *Hub) pushPresenter(c *client) {
	if b, ok := h.presenterMessage(); ok {
		c.trySend(b)
	}
}

// broadcastPresenter refreshes every presenter page.
func (h *Hub) broadcastPresenter() {
	b, ok := h.presenterMessage()
	if !ok {
		return
	}
	for _, c := range h.teachersWhere(func(c *client) bool { return c.presenting.Load() }) {
		c.trySend(b)
	}
}

var navActions = map[string]bool{"next": true, "prev": true, "first": true, "last": true, "pause": true}

var errBadNav = errors.New("commande de navigation inconnue")

var errNoDeck = errors.New("aucun diaporama ouvert : ouvre-le d'abord sur l'ordinateur relié au projecteur")

// relayNav forwards a remote-control command to the open deck(s).
func (h *Hub) relayNav(action string) error {
	if !navActions[action] {
		return errBadNav
	}
	decks := h.teachersWhere(func(c *client) bool { return c.mirroring.Load() })
	if len(decks) == 0 {
		return errNoDeck
	}
	b, ok := h.encode(envelope{T: "nav", D: map[string]string{"action": action}})
	if !ok {
		return errBadNav
	}
	for _, c := range decks {
		c.trySend(b)
	}
	return nil
}

func (h *Hub) teachersWhere(keep func(*client) bool) []*client {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*client
	for c := range h.teachers {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

func (h *Hub) anyMirroring() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.teachers {
		if c.mirroring.Load() {
			return true
		}
	}
	return false
}

func (h *Hub) pushTeacher(c *client) {
	if b, ok := h.encode(envelope{T: "teacher", D: h.room.TeacherView(c.view())}); ok && !c.trySend(b) {
		h.dirty.Store(true)
	}
}

func (h *Hub) flushTeachers() {
	if !h.dirty.Swap(false) {
		return
	}
	h.mu.Lock()
	conns := keys(h.teachers)
	h.mu.Unlock()
	cache := map[string][]byte{}
	for _, c := range conns {
		v := c.view()
		msg, ok := cache[v]
		if !ok {
			b, ok := h.encode(envelope{T: "teacher", D: h.room.TeacherView(v)})
			if !ok {
				continue
			}
			msg = b
			cache[v] = msg
		}
		if !c.trySend(msg) {
			h.dirty.Store(true) // retry on the next tick so the page never stays stale
		}
	}
}

// Run flushes teacher views and applies live rules until ctx is done.
func (h *Hub) Run(ctx context.Context) {
	flush := time.NewTicker(teacherFlushEvery)
	rules := time.NewTicker(rulesEvery)
	defer flush.Stop()
	defer rules.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-flush.C:
			h.safely("flush", h.flushTeachers)
		case <-rules.C:
			h.safely("rules", func() { h.room.ApplyRules(classroom.DefaultThresholds) })
		}
	}
}

// safely keeps the hub alive if a tick panics: losing one frame is better
// than losing the whole in-memory session.
func (h *Hub) safely(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			h.log.Error("hub tick panicked", "tick", name, "panic", r)
		}
	}()
	fn()
}

func (h *Hub) encode(v any) ([]byte, bool) {
	b, err := json.Marshal(v)
	if err != nil {
		h.log.Error("encode frame", "err", err)
		return nil, false
	}
	return b, true
}

// studentConns counts open sockets for a student.
func (h *Hub) studentConns(sid string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.students[sid])
}

func keys(m map[*client]struct{}) []*client {
	out := make([]*client, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // only our own types are marshalled
	}
	return b
}

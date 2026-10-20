package web

import (
	"context"
	"encoding/json"
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
	studentID string // empty for the teacher
	send      chan []byte
	cancel    context.CancelFunc
	mu        sync.Mutex
	viewQID   string
	mirroring atomic.Bool // a deck that publishes slides to students
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
			}
		})
	}
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

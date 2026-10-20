package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"audience-keeper/internal/classroom"
)

const adminToken = "test-admin-token-123456"

type harness struct {
	t    *testing.T
	room *classroom.Classroom
	srv  *httptest.Server
	s    *Server
}

func (h *harness) hubForTest() *Hub { return h.s.hub }

func newHarness(t *testing.T, mcp http.Handler) *harness {
	return newHarnessWithDecks(t, mcp, "")
}

func newHarnessWithDecks(t *testing.T, mcp http.Handler, decksDir string) *harness {
	t.Helper()
	room := classroom.New(classroom.WithCode("ABC123"))
	s, err := New(Config{AdminToken: adminToken, DecksDir: decksDir}, room, mcp, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(func() { cancel(); srv.Close() })
	return &harness{t: t, room: room, srv: srv, s: s}
}

func (h *harness) wsURL(path string) string {
	return "ws" + strings.TrimPrefix(h.srv.URL, "http") + path
}

func (h *harness) join(pseudo string) joinResponse {
	h.t.Helper()
	body, _ := json.Marshal(joinRequest{Code: "abc123", Pseudo: pseudo})
	res, err := httpPost(http.DefaultClient, h.srv.URL+"/api/join", "application/json", bytes.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("join status %d", res.StatusCode)
	}
	var jr joinResponse
	if err := json.NewDecoder(res.Body).Decode(&jr); err != nil {
		h.t.Fatal(err)
	}
	return jr
}

// adminClient logs in and returns an HTTP client carrying the session cookie.
func (h *harness) adminClient() *http.Client {
	h.t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	res, err := httpPostForm(c, h.srv.URL+"/admin/login", url.Values{"token": {adminToken}})
	if err != nil {
		h.t.Fatal(err)
	}
	_ = res.Body.Close()
	return c
}

type wsConn struct {
	t    *testing.T
	conn *websocket.Conn
}

func (h *harness) dial(path string, client *http.Client) *wsConn {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {h.srv.URL}}}
	if client != nil {
		opts.HTTPClient = client
	}
	conn, resp, err := websocket.Dial(ctx, h.wsURL(path), opts)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		h.t.Fatalf("dial %s: %v", path, err)
	}
	conn.SetReadLimit(1 << 20) // browsers have no limit; the Go client defaults to 32 KiB
	h.t.Cleanup(func() { _ = conn.CloseNow() })
	return &wsConn{t: h.t, conn: conn}
}

func (w *wsConn) send(v any) {
	w.t.Helper()
	b, _ := json.Marshal(v)
	if err := w.conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		w.t.Fatal(err)
	}
}

// waitFor reads messages until one of type typ satisfies ok.
func (w *wsConn) waitFor(typ string, ok func(json.RawMessage) bool) json.RawMessage {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, b, err := w.conn.Read(ctx)
		if err != nil {
			w.t.Fatalf("waiting for %q: %v", typ, err)
		}
		var env struct {
			T string          `json:"t"`
			D json.RawMessage `json:"d"`
		}
		if err := json.Unmarshal(b, &env); err != nil {
			w.t.Fatal(err)
		}
		if env.T == typ && (ok == nil || ok(env.D)) {
			return env.D
		}
	}
}

func TestStaticPagesAndHeaders(t *testing.T) {
	h := newHarness(t, nil)
	for path, want := range map[string]string{"/": "Rejoindre la séance", "/admin": "Espace enseignant", "/static/admin.js": "use strict", "/healthz": "ok"} {
		res, err := httpGet(http.DefaultClient, h.srv.URL+path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if !strings.Contains(string(body), want) {
			t.Errorf("%s: missing %q", path, want)
		}
		if res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: security headers missing", path)
		}
	}
	res, _ := httpGet(http.DefaultClient, h.srv.URL+"/mcp")
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("mcp should be disabled, got %d", res.StatusCode)
	}
}

func TestJoinAPI(t *testing.T) {
	h := newHarness(t, nil)
	h.join("Léa")
	tests := []struct {
		name string
		body string
		want int
	}{
		{"bad code", `{"code":"nope","pseudo":"Tom"}`, http.StatusForbidden},
		{"taken", `{"code":"ABC123","pseudo":"léa"}`, http.StatusConflict},
		{"empty pseudo", `{"code":"ABC123","pseudo":""}`, http.StatusBadRequest},
		{"unknown field", `{"code":"ABC123","pseudo":"x","admin":true}`, http.StatusBadRequest},
		{"not json", `hello`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := httpPost(http.DefaultClient, h.srv.URL+"/api/join", "application/json", strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != tt.want {
				t.Fatalf("status %d, want %d", res.StatusCode, tt.want)
			}
		})
	}
}

func TestAdminAuth(t *testing.T) {
	h := newHarness(t, nil)
	for _, p := range []string{"/admin/qr.png", "/admin/export.csv", "/admin/join-url", "/admin/ws"} {
		res, err := httpGet(http.DefaultClient, h.srv.URL+p)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without session: %d", p, res.StatusCode)
		}
	}
	jar, _ := cookiejar.New(nil)
	bad := &http.Client{Jar: jar}
	res, _ := httpPostForm(bad, h.srv.URL+"/admin/login", url.Values{"token": {"wrong"}})
	_ = res.Body.Close()
	if res.Request.URL.Query().Get("error") != "1" {
		t.Fatal("bad token should redirect with error")
	}

	c := h.adminClient()
	res, _ = httpGet(c, h.srv.URL+"/admin")
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), "Nouvelle question") {
		t.Fatal("logged-in admin should see the dashboard")
	}
	res, _ = httpGet(c, h.srv.URL+"/admin/qr.png")
	png, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.Header.Get("Content-Type") != "image/png" || !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatal("qr is not a png")
	}
	res, _ = httpGet(c, h.srv.URL+"/admin/join-url")
	var ju map[string]string
	_ = json.NewDecoder(res.Body).Decode(&ju)
	_ = res.Body.Close()
	if !strings.HasSuffix(ju["url"], "/?code=ABC123") {
		t.Fatalf("join url %q", ju["url"])
	}
	res, _ = httpGet(c, h.srv.URL+"/admin/export.csv")
	csv, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.HasPrefix(string(csv), "question_id,") {
		t.Fatalf("csv: %s", csv)
	}
	res, _ = httpPostForm(c, h.srv.URL+"/admin/logout", nil)
	_ = res.Body.Close()
	res, _ = httpGet(c, h.srv.URL+"/admin/join-url")
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatal("logout should invalidate the session")
	}
}

func TestLiveFlow(t *testing.T) {
	h := newHarness(t, nil)
	teacher := h.dial("/admin/ws", h.adminClient())
	teacher.waitFor("teacher", nil)

	jr := h.join("Léa")
	student := h.dial("/ws", nil)
	student.send(map[string]string{"t": "hello", "token": jr.Token})
	student.waitFor("state", func(d json.RawMessage) bool { return strings.Contains(string(d), `"question":null`) })

	// teacher opens a question, student receives it
	teacher.send(map[string]any{"t": "create", "title": "C'est quoi une goroutine ?", "type": "text", "open": true})
	var sv classroom.StudentView
	raw := student.waitFor("state", func(d json.RawMessage) bool { return strings.Contains(string(d), "goroutine") })
	_ = json.Unmarshal(raw, &sv)
	qid := sv.Question.ID

	// live typing and paste are visible to the teacher
	student.send(map[string]any{"t": "event", "q": qid, "kind": "paste", "chars": 12, "text": "copié collé"})
	student.send(map[string]any{"t": "draft", "q": qid, "text": "un thread léger"})
	teacher.waitFor("teacher", func(d json.RawMessage) bool {
		var tv classroom.TeacherView
		_ = json.Unmarshal(d, &tv)
		return tv.View != nil && len(tv.View.Answers) == 1 && tv.View.Answers[0].Draft == "un thread léger" && tv.View.Answers[0].Pastes == 1
	})

	// invalid messages are reported, not fatal
	student.send(map[string]any{"t": "hack"})
	student.waitFor("error", nil)

	// submit, then teacher validates and gets stats
	student.send(map[string]any{"t": "submit", "q": qid, "value": "un thread léger"})
	student.waitFor("state", func(d json.RawMessage) bool { return strings.Contains(string(d), `"submitted":true`) })
	teacher.send(map[string]any{"t": "close", "q": qid})
	teacher.waitFor("teacher", func(d json.RawMessage) bool {
		var tv classroom.TeacherView
		_ = json.Unmarshal(d, &tv)
		return tv.View != nil && tv.View.Question.Status == classroom.StatusClosed && tv.View.Stats.Answered == 1
	})
	student.waitFor("state", func(d json.RawMessage) bool { return strings.Contains(string(d), `"question":null`) })

	// teacher message reaches the student
	teacher.send(map[string]any{"t": "message", "s": jr.StudentID, "text": "Bravo !"})
	student.waitFor("state", func(d json.RawMessage) bool { return strings.Contains(string(d), "Bravo !") })

	// errors from teacher actions come back to the teacher
	teacher.send(map[string]any{"t": "open", "q": "q999"})
	teacher.waitFor("error", nil)

	// reset ends student sessions
	teacher.send(map[string]any{"t": "reset"})
	student.waitFor("ended", nil)
}

func TestStudentWSRejectsBadToken(t *testing.T) {
	h := newHarness(t, nil)
	student := h.dial("/ws", nil)
	student.send(map[string]string{"t": "hello", "token": "forged"})
	student.waitFor("ended", nil)
}

func TestTeacherActions(t *testing.T) {
	h := newHarness(t, nil)
	teacher := h.dial("/admin/ws", h.adminClient())
	teacher.waitFor("teacher", nil)
	jr := h.join("Tom")

	teacher.send(map[string]any{"t": "create", "type": "choice", "choices": 3})
	teacher.waitFor("teacher", func(d json.RawMessage) bool { return strings.Contains(string(d), `"status":"proposed"`) })
	q := h.room.Questions()[0]
	steps := []map[string]any{
		{"t": "open", "q": q.ID},
		{"t": "correct", "q": q.ID, "values": []string{"B"}},
		{"t": "grade", "q": q.ID, "s": jr.StudentID, "grade": "partial"},
		{"t": "aimode", "mode": "auto"},
		{"t": "view", "q": q.ID},
	}
	for _, m := range steps {
		teacher.send(m)
	}
	teacher.waitFor("teacher", func(d json.RawMessage) bool {
		var tv classroom.TeacherView
		_ = json.Unmarshal(d, &tv)
		return tv.AIMode == classroom.AIModeAuto && tv.View != nil && len(tv.View.Question.Correct) == 1 && len(tv.View.Answers) == 1
	})

	sg, err := h.room.SuggestMessage(jr.StudentID, "indice")
	if err != nil || sg.Status != classroom.SuggestionSent {
		t.Fatalf("auto mode: %+v %v", sg, err)
	}
	_ = h.room.SetAIMode(classroom.AIModeSuggest)
	sg, _ = h.room.SuggestMessage(jr.StudentID, "autre indice")
	teacher.send(map[string]any{"t": "approve", "id": sg.ID, "text": "indice corrigé"})
	sg2, _ := h.room.SuggestMessage(jr.StudentID, "à ignorer")
	teacher.send(map[string]any{"t": "dismiss", "id": sg2.ID})
	teacher.send(map[string]any{"t": "close", "q": q.ID})
	teacher.waitFor("teacher", func(d json.RawMessage) bool { return strings.Contains(string(d), `"status":"closed"`) })

	msgs := h.room.MessagesFor(jr.StudentID)
	if len(msgs) != 2 || msgs[1].Text != "indice corrigé" {
		t.Fatalf("messages: %+v", msgs)
	}
	q2, _ := h.room.CreateQuestion(classroom.NewQuestion{Type: classroom.TypeText})
	teacher.send(map[string]any{"t": "delete", "q": q2.ID})
	teacher.waitFor("teacher", func(d json.RawMessage) bool { return !strings.Contains(string(d), `"id":"`+q2.ID+`"`) })
}

func TestRequireBearer(t *testing.T) {
	h := RequireBearer("secret-token-0123456789", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	for _, tt := range []struct {
		header string
		want   int
	}{
		{"", http.StatusUnauthorized},
		{"Bearer nope", http.StatusUnauthorized},
		{"secret-token-0123456789", http.StatusUnauthorized},
		{"Bearer secret-token-0123456789", http.StatusTeapot},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", nil)
		if tt.header != "" {
			req.Header.Set("Authorization", tt.header)
		}
		h.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Errorf("%q: %d, want %d", tt.header, rec.Code, tt.want)
		}
	}
}

func TestLoginRateLimit(t *testing.T) {
	a := newAdminAuth(adminToken)
	for i := 0; i < 5; i++ {
		a.login(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil), "wrong")
	}
	if a.login(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil), adminToken) {
		t.Fatal("brute force should be throttled, even with the right token")
	}
}

func TestLoginLimiterIsPerIPAndFailuresOnly(t *testing.T) {
	a := newAdminAuth(adminToken)
	req := func(ip string) *http.Request {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
		r.RemoteAddr = ip + ":1234"
		return r
	}
	for i := 0; i < 10; i++ {
		a.login(httptest.NewRecorder(), req("10.0.0.66"), "wrong")
	}
	if !a.login(httptest.NewRecorder(), req("10.0.0.1"), adminToken) {
		t.Fatal("attacker on another IP must not lock the teacher out")
	}
	for i := 0; i < 10; i++ {
		if !a.login(httptest.NewRecorder(), req("10.0.0.2"), adminToken) {
			t.Fatal("successful logins must not consume the budget")
		}
	}
}

func TestBearerSchemeCaseInsensitive(t *testing.T) {
	h := RequireBearer("secret-token-0123456789", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "bearer secret-token-0123456789")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Fatalf("%d", rec.Code)
	}
}

func TestTeacherSocketClosedOnLogout(t *testing.T) {
	h := newHarness(t, nil)
	c := h.adminClient()
	teacher := h.dial("/admin/ws", c)
	teacher.waitFor("teacher", nil)
	res, _ := httpPostForm(c, h.srv.URL+"/admin/logout", nil)
	_ = res.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), sessionRecheck+3*time.Second)
	defer cancel()
	for {
		if _, _, err := teacher.conn.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("socket still open after logout")
			}
			return
		}
	}
}

func TestStudentClosedAfterBadMessages(t *testing.T) {
	h := newHarness(t, nil)
	jr := h.join("Spam")
	st := h.dial("/ws", nil)
	st.send(map[string]string{"t": "hello", "token": jr.Token})
	st.waitFor("state", nil)
	for i := 0; i < maxBadMessages; i++ {
		_ = st.conn.Write(context.Background(), websocket.MessageText, []byte("{not json"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := st.conn.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("expected policy close, got %v", err)
			}
			return
		}
	}
}

func TestDecks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cours.html"), []byte("<p>mon cours</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarnessWithDecks(t, nil, dir)

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, _ := httpGet(noRedirect, h.srv.URL+"/deck/demo.html")
	_ = res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/admin?next=%2Fdeck%2Fdemo.html" {
		t.Fatalf("anonymous deck access: %d %s", res.StatusCode, res.Header.Get("Location"))
	}

	c := h.adminClient()
	get := func(p string) (int, string, http.Header) {
		res, err := httpGet(c, h.srv.URL+p)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b), res.Header
	}
	code, body, hdr := get("/deck/demo.html")
	if code != 200 || !strings.Contains(body, "data-ak-question") || hdr.Get("Content-Security-Policy") != deckCSP || hdr.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("demo deck: %d %v", code, hdr)
	}
	if code, body, _ := get("/deck/cours.html"); code != 200 || !strings.Contains(body, "mon cours") {
		t.Fatalf("user deck: %d", code)
	}
	if code, body, _ := get("/deck/"); code != 200 || !strings.Contains(body, "cours.html") || !strings.Contains(body, "demo.html") {
		t.Fatalf("index: %d %s", code, body)
	}
	for _, p := range []string{"/deck/../secret.txt", "/deck/%2e%2e/secret.txt", "/deck/nope.html"} {
		if code, body, _ := get(p); code == 200 && strings.Contains(body, "secret") {
			t.Fatalf("%s escaped the decks directory", p)
		}
	}
	if code, _, _ := get("/static/vendor/reveal/reveal.js"); code != 200 {
		t.Fatal("reveal.js not embedded")
	}
	if code, _, _ := get("/static/reveal-ak.js"); code != 200 {
		t.Fatal("plugin not served")
	}
}

func TestNewFailsOnMissingDecksDir(t *testing.T) {
	_, err := New(Config{AdminToken: adminToken, DecksDir: "/nonexistent/decks"}, classroom.New(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoginRedirectsToNext(t *testing.T) {
	for in, want := range map[string]string{"/deck/demo.html": "/deck/demo.html", "https://evil.example": "/admin", "//evil.example": "/admin", "": "/admin", `/\evil`: "/admin"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
	h := newHarness(t, nil)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	res, err := httpPostForm(c, h.srv.URL+"/admin/login", url.Values{"token": {adminToken}, "next": {"/deck/demo.html"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.Request.URL.Path != "/deck/demo.html" || res.StatusCode != 200 {
		t.Fatalf("landed on %s (%d)", res.Request.URL, res.StatusCode)
	}
}

func TestSlideQuestionsViaWS(t *testing.T) {
	h := newHarness(t, nil)
	deck := h.dial("/admin/ws", h.adminClient())
	deck.waitFor("teacher", nil)
	for i := 0; i < 2; i++ { // reconnect / revisit sends the same key again
		deck.send(map[string]any{"t": "create", "key": "slide:4.0", "title": "Q", "type": "yesno", "open": true})
	}
	deck.waitFor("teacher", func(d json.RawMessage) bool { return strings.Contains(string(d), `"key":"slide:4.0"`) })
	if qs := h.room.Questions(); len(qs) != 1 || qs[0].Status != classroom.StatusOpen {
		t.Fatalf("%+v", qs)
	}
}

func TestDeckAssetsPublicPagesPrivate(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "cours.html"), []byte("<section>secret</section>"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "img.svg"), []byte("<svg/>"), 0o600)
	h := newHarnessWithDecks(t, nil, dir)
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for p, want := range map[string]int{"/deck/img.svg": 200, "/deck/cours.html": 303, "/deck/COURS.HTML": 303, "/deck/": 303, "/deck/sub": 303} {
		res, err := httpGet(noRedirect, h.srv.URL+p)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("%s: %d, want %d", p, res.StatusCode, want)
		}
	}
	res, _ := httpGet(http.DefaultClient, h.srv.URL+"/")
	_ = res.Body.Close()
	if res.Header.Get("Content-Security-Policy") != studentCSP || strings.Contains(studentCSP, "script-src 'self' 'unsafe-inline'") {
		t.Fatal("student page must allow slide styles but never inline scripts")
	}
}

func TestSlideMirroredToStudents(t *testing.T) {
	h := newHarness(t, nil)
	deck := h.dial("/admin/ws", h.adminClient())
	deck.waitFor("teacher", nil)
	jr := h.join("Léa")
	st := h.dial("/ws", nil)
	st.send(map[string]string{"t": "hello", "token": jr.Token})
	st.waitFor("state", nil)

	// > 64 KiB (student limit), < 256 KiB; no '<' in the filler since Go's JSON escapes it to 6 bytes
	big := "<section><h2>Grosse slide</h2><p>" + strings.Repeat("x", 150_000) + "</p></section>"
	deck.send(map[string]any{"t": "slide", "deck": "Cours", "html": big, "h": 3, "fragment": 1, "themes": []string{"/static/vendor/reveal/theme/white.css"}})
	raw := st.waitFor("state", func(d json.RawMessage) bool { return strings.Contains(string(d), "Grosse slide") })
	var sv classroom.StudentView
	_ = json.Unmarshal(raw, &sv)
	if sv.Slide == nil || sv.Slide.H != 3 || sv.Slide.Fragment != 1 {
		t.Fatalf("%+v", sv.Slide)
	}
	deck.send(map[string]any{"t": "slide", "html": "<section/>", "themes": []string{"https://evil.example/a.css"}})
	deck.waitFor("error", nil)
	deck.send(map[string]any{"t": "slide-off"})
	st.waitFor("state", func(d json.RawMessage) bool { return strings.Contains(string(d), `"slide":null`) })
}

func TestSlideClearedWhenDeckDisconnects(t *testing.T) {
	h := newHarness(t, nil)
	h.hubForTest().mirrorGrace = 50 * time.Millisecond
	deck := h.dial("/admin/ws", h.adminClient())
	deck.waitFor("teacher", nil)
	deck.send(map[string]any{"t": "slide", "html": "<section>a</section>"})
	deck.waitFor("teacher", func(d json.RawMessage) bool { return strings.Contains(string(d), `"live":{`) })
	_ = deck.conn.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := h.room.Slide(); !ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("slide still mirrored after the deck left")
}

// HTTP helpers: every request carries a context (noctx).

func httpGet(c *http.Client, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

func httpPost(c *http.Client, u, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}

func httpPostForm(c *http.Client, u string, v url.Values) (*http.Response, error) {
	return httpPost(c, u, "application/x-www-form-urlencoded", strings.NewReader(v.Encode()))
}

func TestPresenterRemoteControl(t *testing.T) {
	h := newHarness(t, nil)
	deck := h.dial("/admin/ws", h.adminClient())
	deck.waitFor("teacher", nil)
	jr := h.join("Léa")
	st := h.dial("/ws", nil)
	st.send(map[string]string{"t": "hello", "token": jr.Token})
	st.waitFor("state", nil)

	// without an open deck, the remote gets a clear error
	phone := h.dial("/admin/ws", h.adminClient())
	phone.waitFor("teacher", nil)
	phone.send(map[string]any{"t": "presenter"})
	phone.waitFor("presenter", func(d json.RawMessage) bool { return strings.Contains(string(d), `"live":false`) })
	phone.send(map[string]any{"t": "nav", "action": "next"})
	phone.waitFor("error", func(d json.RawMessage) bool { return strings.Contains(string(d), "aucun diaporama") })

	// the deck publishes a slide with teacher-only details
	deck.send(map[string]any{"t": "slide", "deck": "Cours", "html": "<section>courante</section>", "fragment": -1,
		"notes": "NOTE-SECRETE", "nextHtml": "<section>SUIVANTE</section>", "number": 2, "total": 5, "questionKey": "k"})
	phone.waitFor("presenter", func(d json.RawMessage) bool {
		return strings.Contains(string(d), "NOTE-SECRETE") && strings.Contains(string(d), "SUIVANTE")
	})
	raw := st.waitFor("state", func(d json.RawMessage) bool { return strings.Contains(string(d), "courante") })
	if strings.Contains(string(raw), "NOTE-SECRETE") || strings.Contains(string(raw), "SUIVANTE") {
		t.Fatalf("notes or next slide sent to a student: %s", raw)
	}

	// the phone drives the deck through the server
	phone.send(map[string]any{"t": "nav", "action": "next"})
	deck.waitFor("nav", func(d json.RawMessage) bool { return strings.Contains(string(d), `"next"`) })
	phone.send(map[string]any{"t": "nav", "action": "rm -rf"})
	phone.waitFor("error", nil)

	// the deck closes: presenter pages are told
	deck.send(map[string]any{"t": "slide-off"})
	phone.waitFor("presenter", func(d json.RawMessage) bool { return strings.Contains(string(d), `"live":false`) })
}

func TestPhonePairing(t *testing.T) {
	h := newHarness(t, nil)
	c := h.adminClient()
	res, err := httpGet(c, h.srv.URL+"/admin/pair.png")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("pair qr: %d", res.StatusCode)
	}
	if code := statusOf(t, http.DefaultClient, h.srv.URL+"/admin/pair.png"); code != http.StatusUnauthorized {
		t.Fatal("pairing QR must require a teacher session")
	}

	tok := h.s.auth.newPairing()
	jar, _ := cookiejar.New(nil)
	phone := &http.Client{Jar: jar}
	res, _ = httpGet(phone, h.srv.URL+"/admin/pair?t="+tok)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), "/admin/presenter") {
		t.Fatalf("pairing: %d %s", res.StatusCode, body)
	}
	res, _ = httpGet(phone, h.srv.URL+"/admin/presenter")
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), "Présentateur") || res.Header.Get("Content-Security-Policy") != studentCSP {
		t.Fatalf("paired phone should reach the presenter view: %d", res.StatusCode)
	}

	// single use, and expired tokens are refused
	other := &http.Client{}
	if statusOf(t, other, h.srv.URL+"/admin/pair?t="+tok) != http.StatusForbidden {
		t.Fatal("a pairing token must work only once")
	}
	old := h.s.auth.newPairing()
	h.s.auth.now = func() time.Time { return time.Now().Add(pairingTTL + time.Second) }
	if statusOf(t, other, h.srv.URL+"/admin/pair?t="+old) != http.StatusForbidden {
		t.Fatal("an expired pairing token must be refused")
	}
}

func statusOf(t *testing.T, c *http.Client, u string) int {
	t.Helper()
	res, err := httpGet(c, u)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

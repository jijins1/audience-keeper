// Package web serves the student and teacher pages, their WebSocket
// endpoints and, optionally, the MCP endpoint.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/skip2/go-qrcode"
	"golang.org/x/time/rate"

	"audience-keeper/internal/classroom"
)

//go:embed static
var staticFS embed.FS

// Config holds the web settings.
type Config struct {
	AdminToken     string
	PublicURL      string   // base URL students use, e.g. https://quiz.example.org
	AllowedOrigins []string // extra WebSocket origins (host patterns)
	DecksDir       string   // optional directory of reveal.js presentations
}

// Server is the HTTP front of a classroom.
type Server struct {
	cfg     Config
	room    *classroom.Classroom
	hub     *Hub
	auth    *adminAuth
	joinLim *rate.Limiter
	mcp     http.Handler
	log     *slog.Logger
	static  fs.FS
	assets  *assets
	decks   *decks
}

// New builds a server. mcpHandler may be nil to disable MCP; it must already
// enforce its own authentication.
func New(cfg Config, room *classroom.Classroom, mcpHandler http.Handler, log *slog.Logger) (*Server, error) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("static files: %w", err)
	}
	as, err := loadAssets(sub)
	if err != nil {
		return nil, fmt.Errorf("static files: %w", err)
	}
	dk, err := newDecks(cfg.DecksDir)
	if err != nil {
		return nil, fmt.Errorf("decks directory: %w", err)
	}
	return &Server{
		decks:   dk,
		cfg:     cfg,
		room:    room,
		hub:     NewHub(room, log),
		auth:    newAdminAuth(cfg.AdminToken),
		joinLim: rate.NewLimiter(50, 200),
		mcp:     mcpHandler,
		log:     log,
		static:  sub,
		assets:  as,
	}, nil
}

// Run drives the real-time hub until ctx is cancelled.
func (s *Server) Run(ctx context.Context) { s.hub.Run(ctx) }

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", studentCSP)
		s.page("student.html")(w, r)
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", s.assets))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("POST /api/join", s.handleJoin)
	mux.HandleFunc("GET /ws", s.handleStudentWS)

	mux.HandleFunc("GET /admin", s.handleAdminPage)
	mux.HandleFunc("POST /admin/login", s.handleLogin)
	mux.HandleFunc("POST /admin/logout", s.handleLogout)
	mux.Handle("GET /admin/ws", s.auth.require(http.HandlerFunc(s.handleTeacherWS)))
	mux.Handle("GET /admin/qr.png", s.auth.require(http.HandlerFunc(s.handleQR)))
	mux.Handle("GET /admin/join-url", s.auth.require(http.HandlerFunc(s.handleJoinURL)))
	mux.Handle("GET /admin/export.csv", s.auth.require(http.HandlerFunc(s.handleExport)))
	// Deck pages are teacher-only (they contain every upcoming slide); their
	// assets (images, CSS, media) are public so mirrored slides render for students.
	mux.Handle("GET /deck/", s.deckRoute())

	// Presenter view (laptop) and remote control (phone): same page.
	mux.Handle("GET /admin/presenter", s.auth.requirePage(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", studentCSP) // renders slides with their themes
		s.page("presenter.html")(w, r)
	})))
	mux.Handle("GET /admin/pair.png", s.auth.require(http.HandlerFunc(s.handlePairQR)))
	mux.HandleFunc("GET /admin/pair", s.handlePair)

	if s.mcp != nil {
		mux.Handle("/mcp", s.mcp)
	}
	return securityHeaders(mux)
}

func (s *Server) deckRoute() http.Handler {
	pages := s.auth.requirePage(s.decks)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.ToLower(path.Clean(r.URL.Path))
		if p == "/deck" || strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".htm") || !strings.Contains(path.Base(p), ".") {
			pages.ServeHTTP(w, r)
			return
		}
		s.decks.ServeHTTP(w, r)
	})
}

// studentCSP lets mirrored slides use their themes, inline styles, images and
// embeds, but still runs no script other than our own.
const studentCSP = "default-src 'self'; script-src 'self'; " +
	"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://unpkg.com https://cdnjs.cloudflare.com https://fonts.googleapis.com; " +
	"font-src 'self' data: https:; img-src 'self' data: blob: https:; media-src 'self' https:; frame-src https:; " +
	"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFileFS(w, r, s.static, name)
	}
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if s.auth.valid(r) {
		s.page("admin.html")(w, r)
		return
	}
	s.page("login.html")(w, r)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "requête invalide", http.StatusBadRequest)
		return
	}
	if !s.auth.login(w, r, r.PostForm.Get("token")) {
		s.log.Warn("admin login failed", "remote", r.RemoteAddr)
		http.Redirect(w, r, "/admin?error=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, safeNext(r.PostForm.Get("next")), http.StatusSeeOther) //nolint:gosec // G710: safeNext n'accepte que des chemins locaux (testé)
}

// safeNext only allows local paths, so the login form cannot be used as an open redirect.
func safeNext(next string) string {
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.Contains(next, "\\") {
		return next
	}
	return "/admin"
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.logout(w, r)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

type joinRequest struct {
	Code   string `json:"code"`
	Pseudo string `json:"pseudo"`
}

type joinResponse struct {
	Token     string `json:"token"`
	StudentID string `json:"studentId"`
	Pseudo    string `json:"pseudo"`
}

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	if !s.joinLim.Allow() {
		writeError(w, http.StatusTooManyRequests, "trop de tentatives, réessaie dans un instant")
		return
	}
	var req joinRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "requête invalide")
		return
	}
	st, token, err := s.room.Join(req.Code, req.Pseudo)
	switch {
	case errors.Is(err, classroom.ErrBadCode):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, classroom.ErrFull):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, classroom.ErrPseudoTaken):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusOK, joinResponse{Token: token, StudentID: st.ID, Pseudo: st.Pseudo})
	}
}

func (s *Server) baseURL(r *http.Request) string {
	if base := strings.TrimRight(s.cfg.PublicURL, "/"); base != "" {
		return base
	}
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) joinURL(r *http.Request) string {
	return s.baseURL(r) + "/?code=" + url.QueryEscape(s.room.Code())
}

func (s *Server) handleJoinURL(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"url": s.joinURL(r), "code": s.room.Code()})
}

func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	png, err := qrcode.Encode(s.joinURL(r), qrcode.Medium, 512)
	if err != nil {
		s.log.Error("qr encode", "err", err)
		http.Error(w, "erreur QR code", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

// handlePairQR shows a one-time QR code that logs the phone in as the teacher.
func (s *Server) handlePairQR(w http.ResponseWriter, r *http.Request) {
	u := s.baseURL(r) + "/admin/pair?t=" + url.QueryEscape(s.auth.newPairing())
	png, err := qrcode.Encode(u, qrcode.Medium, 512)
	if err != nil {
		s.log.Error("pair qr encode", "err", err)
		http.Error(w, "erreur QR code", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	if !s.auth.pair(w, r, r.URL.Query().Get("t")) {
		s.log.Warn("phone pairing refused", "remote", r.RemoteAddr)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("QR code expiré ou déjà utilisé : affiches-en un nouveau depuis la vue présentateur."))
		return
	}
	// Not an HTTP redirect: the session cookie is SameSite=Strict, and a redirect
	// that continues a navigation started outside the site (the QR scanner) may
	// be sent without it. A same-origin refresh is a same-site navigation.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(`<!doctype html><html lang="fr"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="0;url=/admin/presenter"><title>Télécommande</title>
<link rel="stylesheet" href="/static/style.css"></head>
<body><main class="login panel"><h1>Téléphone connecté ✓</h1>
<p><a href="/admin/presenter">Ouvrir la télécommande</a></p></main></body></html>`))
}

func (s *Server) handleExport(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="seance-`+s.room.StartedAt().Format("2006-01-02-1504")+`.csv"`)
	if err := s.room.ExportCSV(w); err != nil {
		s.log.Error("csv export", "err", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

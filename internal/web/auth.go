package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	adminCookie     = "ak_admin"
	adminSessionTTL = 12 * time.Hour
)

// adminAuth issues and checks teacher session cookies.
type adminAuth struct {
	token    []byte
	failures *ipLimiter // failed login attempts, per client IP
	mu       sync.Mutex
	session  map[string]time.Time // sha256(session id) -> expiry
	rev      int64                // bumped when sessions change (persistence)
	pairing  map[string]time.Time // one-time phone pairing token -> expiry
	now      func() time.Time
}

// pairingTTL bounds how long a pairing QR code stays usable.
const pairingTTL = 2 * time.Minute

func newAdminAuth(token string) *adminAuth {
	return &adminAuth{
		token:    []byte(token),
		failures: newIPLimiter(rate.Every(10*time.Second), 5),
		session:  map[string]time.Time{},
		pairing:  map[string]time.Time{},
		now:      time.Now,
	}
}

// login only spends rate-limit budget on failures, per IP, so a student
// spamming bad tokens cannot lock the teacher out from another address.
func (a *adminAuth) login(w http.ResponseWriter, r *http.Request, token string) bool {
	ip := clientIP(r)
	if a.failures.exhausted(ip) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(token), a.token) != 1 {
		a.failures.allow(ip)
		return false
	}
	a.startSession(w, r)
	return true
}

// startSession issues a fresh teacher session cookie.
func (a *adminAuth) startSession(w http.ResponseWriter, r *http.Request) {
	id := randomID()
	a.mu.Lock()
	now := a.now()
	for k, exp := range a.session {
		if now.After(exp) {
			delete(a.session, k)
		}
	}
	a.session[hashID(id)] = now.Add(adminSessionTTL)
	a.rev++
	a.mu.Unlock()
	// Secure suit le transport : en HTTP simple (wifi de salle, laptop) un cookie
	// Secure ne serait jamais renvoyé et la connexion deviendrait impossible.
	// HttpOnly et SameSite=Strict, eux, sont toujours posés.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure dépend volontairement du transport, voir ci-dessus
		Name:     adminCookie,
		Value:    id,
		Path:     "/",
		MaxAge:   int(adminSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// newPairing issues a one-time token that logs a second device (the phone)
// in. Only a logged-in teacher can obtain one.
func (a *adminAuth) newPairing() string {
	t := randomID()
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for k, exp := range a.pairing {
		if now.After(exp) {
			delete(a.pairing, k)
		}
	}
	a.pairing[t] = now.Add(pairingTTL)
	return t
}

// pair consumes a pairing token: valid once, before it expires.
func (a *adminAuth) pair(w http.ResponseWriter, r *http.Request, token string) bool {
	ip := clientIP(r)
	if a.failures.exhausted(ip) {
		return false
	}
	a.mu.Lock()
	exp, ok := a.pairing[token]
	delete(a.pairing, token)
	valid := ok && a.now().Before(exp)
	a.mu.Unlock()
	if !valid {
		a.failures.allow(ip)
		return false
	}
	a.startSession(w, r)
	return true
}

func (a *adminAuth) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(adminCookie); err == nil {
		a.mu.Lock()
		delete(a.session, hashID(c.Value))
		a.rev++
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: "", Path: "/", MaxAge: -1, //nolint:gosec // G124: même règle que login
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteStrictMode})
}

func (a *adminAuth) valid(r *http.Request) bool {
	return a.validID(sessionID(r))
}

func (a *adminAuth) validID(id string) bool {
	if id == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.session[hashID(id)]
	return ok && a.now().Before(exp)
}

// hashID is how session ids are kept, in memory and in the state file: the
// cookie value itself is never stored.
func hashID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// adminState is what survives a restart: the teacher stays logged in (deck on
// the projector, presenter view, phone). Pairing codes do not survive.
type adminState struct {
	Sessions map[string]time.Time `json:"sessions"`
}

// Revision changes whenever sessions change.
func (a *adminAuth) Revision() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rev
}

// MarshalState serialises the live teacher sessions (hashed ids).
func (a *adminAuth) MarshalState() ([]byte, int64, error) {
	a.mu.Lock()
	s := adminState{Sessions: make(map[string]time.Time, len(a.session))}
	now := a.now()
	for h, exp := range a.session {
		if now.Before(exp) {
			s.Sessions[h] = exp
		}
	}
	rev := a.rev
	a.mu.Unlock()
	data, err := json.Marshal(s)
	return data, rev, err
}

// RestoreState brings back saved teacher sessions that have not expired.
func (a *adminAuth) RestoreState(data []byte) error {
	var s adminState
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("sessions enseignant : %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for h, exp := range s.Sessions {
		if len(h) == sha256.Size*2 && now.Before(exp) && exp.Before(now.Add(adminSessionTTL+time.Minute)) {
			a.session[h] = exp
		}
	}
	a.rev++
	return nil
}

func sessionID(r *http.Request) string {
	c, err := r.Cookie(adminCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func (a *adminAuth) require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.valid(r) {
			http.Error(w, "non autorisé", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requirePage is require for pages: it redirects to the login form.
func (a *adminAuth) requirePage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.valid(r) {
			http.Redirect(w, r, "/admin?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireBearer protects an endpoint with a static bearer token.
func RequireBearer(token string, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, got, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="audience-keeper"`)
			http.Error(w, "non autorisé", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// ipLimiter keeps one token bucket per client IP.
type ipLimiter struct {
	mu    sync.Mutex
	every rate.Limit
	burst int
	m     map[string]*rate.Limiter
}

func newIPLimiter(every rate.Limit, burst int) *ipLimiter {
	return &ipLimiter{every: every, burst: burst, m: map[string]*rate.Limiter{}}
}

func (l *ipLimiter) get(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.m) > 10000 { // crude bound; buckets refill anyway
		l.m = map[string]*rate.Limiter{}
	}
	lim, ok := l.m[ip]
	if !ok {
		lim = rate.NewLimiter(l.every, l.burst)
		l.m[ip] = lim
	}
	return lim
}

func (l *ipLimiter) allow(ip string) bool { return l.get(ip).Allow() }

func (l *ipLimiter) exhausted(ip string) bool { return l.get(ip).Tokens() < 1 }

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

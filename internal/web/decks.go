package web

import (
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
)

//go:embed decks
var demoDecksFS embed.FS

// deckCSP is looser than the app CSP: decks are written by the teacher and
// commonly use inline Reveal.initialize() calls, inline styles and CDN assets.
// Student-provided data is only ever inserted with textContent by the plugin.
const deckCSP = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://unpkg.com https://cdnjs.cloudflare.com; " +
	"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://unpkg.com https://cdnjs.cloudflare.com https://fonts.googleapis.com; " +
	"font-src 'self' data: https:; img-src 'self' data: blob: https:; media-src 'self' https:; frame-src 'self' https:; " +
	// raw.githubusercontent.com: code of a code question taken from a public
	// repository (data-ak-context-src), fetched by the teacher's deck page.
	"connect-src 'self' https://raw.githubusercontent.com; frame-ancestors 'self'; base-uri 'self'; form-action 'self'"

// decks serves reveal.js presentations: the teacher's directory first, then
// the embedded demo.
type decks struct {
	user fs.FS // nil when no directory is configured
	demo fs.FS
}

func newDecks(dir string) (*decks, error) {
	demo, err := fs.Sub(demoDecksFS, "decks")
	if err != nil {
		return nil, err
	}
	d := &decks{demo: demo}
	if dir != "" {
		// os.Root confines lookups (including symlinks) to the directory.
		root, err := os.OpenRoot(dir)
		if err != nil {
			return nil, err
		}
		d.user = root.FS()
	}
	return d, nil
}

func (d *decks) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", deckCSP)
	h.Set("X-Frame-Options", "SAMEORIGIN") // reveal's speaker view frames the deck
	h.Set("Cache-Control", "no-store")

	name := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(r.URL.Path, "/deck")), "/")
	if name == "" {
		d.index(w)
		return
	}
	for _, fsys := range []fs.FS{d.user, d.demo} {
		if fsys == nil {
			continue
		}
		if st, err := fs.Stat(fsys, name); err == nil && !st.IsDir() {
			http.ServeFileFS(w, r, fsys, name)
			return
		}
	}
	http.NotFound(w, r)
}

var deckIndex = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="fr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Diaporamas</title><link rel="stylesheet" href="/static/style.css"></head>
<body><main class="login panel">
<h1>Diaporamas</h1>
{{if .Decks}}<ul>{{range .Decks}}<li><a href="/deck/{{.}}">{{.}}</a></li>{{end}}</ul>
{{else}}<p class="muted">Aucun diaporama trouvé.</p>{{end}}
{{if not .HasDir}}<p class="muted">Définis <code>AK_DECKS_DIR</code> pour servir tes propres fichiers reveal.js.</p>{{end}}
<p><a href="/admin">← Tableau de bord</a></p>
</main></body></html>`))

func (d *decks) index(w http.ResponseWriter) {
	seen := map[string]bool{}
	var list []string
	for _, fsys := range []fs.FS{d.user, d.demo} {
		if fsys == nil {
			continue
		}
		matches, err := fs.Glob(fsys, "*.html")
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				list = append(list, m)
			}
		}
	}
	sort.Strings(list)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = deckIndex.Execute(w, struct {
		Decks  []string
		HasDir bool
	}{list, d.user != nil})
}

package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// compressible lists the static types worth gzipping (the code editor and
// reveal.js weigh hundreds of KiB and a whole class loads them at once).
var compressible = map[string]bool{".js": true, ".css": true, ".html": true, ".svg": true, ".json": true, ".txt": true}

const minGzipSize = 1 << 10

type asset struct {
	data  []byte
	gz    []byte // nil when not worth it
	etag  string
	ctype string
}

// assets serves the embedded static files from memory, gzipped when the
// client accepts it, with an ETag so reloads are answered with 304.
type assets struct {
	files map[string]*asset
}

func loadAssets(fsys fs.FS) (*assets, error) {
	a := &assets{files: map[string]*asset{}}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		ext := strings.ToLower(path.Ext(p))
		f := &asset{data: data, etag: `"` + hex.EncodeToString(sum[:12]) + `"`, ctype: mime.TypeByExtension(ext)}
		if f.ctype == "" {
			f.ctype = "application/octet-stream"
		}
		if compressible[ext] && len(data) >= minGzipSize {
			if f.gz, err = gzipBytes(data); err != nil {
				return fmt.Errorf("gzip %s: %w", p, err)
			}
			if len(f.gz) >= len(data) {
				f.gz = nil
			}
		}
		a.files[p] = f
		return nil
	})
	return a, err
}

func gzipBytes(data []byte) ([]byte, error) {
	var b bytes.Buffer
	w, err := gzip.NewWriterLevel(&b, gzip.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(enc), "gzip") && strings.ReplaceAll(strings.TrimSpace(q), " ", "") != "q=0" {
			return true
		}
	}
	return false
}

func (a *assets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f, ok := a.files[strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.ctype)
	h.Set("Cache-Control", "no-cache") // always revalidated: a new release is picked up at once
	h.Set("Vary", "Accept-Encoding")
	data, etag := f.data, f.etag
	if f.gz != nil && acceptsGzip(r) {
		data, etag = f.gz, strings.TrimSuffix(f.etag, `"`)+`-gz"`
		h.Set("Content-Encoding", "gzip")
	}
	h.Set("ETag", etag)
	// ServeContent answers If-None-Match with 304 and supports ranges.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

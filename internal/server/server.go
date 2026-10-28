// Package server exposes a read-only local HTTP API and the embedded SPA. It
// never writes to the database, never listens on anything but a caller-provided
// loopback listener, and requires a per-process token on every API call.
package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"fattrace/internal/model"
	"fattrace/internal/rules"
	"fattrace/internal/store"
)

//go:embed web
var webFS embed.FS

const tokenHeader = "X-FatTrace-Token"

// Options configures ranking sizes and the auth token.
type Options struct {
	TopN      int
	Token     string
	DirLimit  int
	FileLimit int
}

// Server serves the SPA and the read-only JSON API.
type Server struct {
	st        *store.Store
	token     string
	topN      int
	dirLimit  int
	fileLimit int
	rules     *rules.Set
	mux       *http.ServeMux

	assets map[string]staticAsset

	diffMu    sync.Mutex
	diffCache map[diffKey]model.DiffResult
}

type staticAsset struct {
	body  []byte
	ctype string
}

type diffKey struct {
	from, to int64
	topN     int
	cross    bool
	// latest makes auto-pair keys (from/to 0) roll over after a new scan.
	latest int64
}

// New builds a server around an already-open read-only store.
func New(st *store.Store, opts Options) (*Server, error) {
	if opts.TopN <= 0 {
		opts.TopN = 20
	}
	if opts.TopN > 500 {
		opts.TopN = 500
	}
	if opts.DirLimit <= 0 {
		opts.DirLimit = 500
	}
	if opts.FileLimit <= 0 {
		opts.FileLimit = 200
	}
	if opts.Token == "" {
		var err error
		if opts.Token, err = NewToken(); err != nil {
			return nil, err
		}
	}
	set, err := rules.Load(st.Path())
	if err != nil {
		return nil, err
	}
	s := &Server{
		st:        st,
		token:     opts.Token,
		topN:      opts.TopN,
		dirLimit:  opts.DirLimit,
		fileLimit: opts.FileLimit,
		rules:     set,
		mux:       http.NewServeMux(),
		assets:    map[string]staticAsset{},
		diffCache: map[diffKey]model.DiffResult{},
	}
	if err := s.loadAssets(); err != nil {
		return nil, err
	}

	s.mux.HandleFunc("GET /{$}", s.serveAsset("/index.html"))
	s.mux.HandleFunc("GET /app.js", s.serveAsset("/app.js"))
	s.mux.HandleFunc("GET /app.css", s.serveAsset("/app.css"))

	s.mux.HandleFunc("GET /api/status", s.auth(s.handleStatus))
	s.mux.HandleFunc("GET /api/snapshots", s.auth(s.handleSnapshots))
	s.mux.HandleFunc("GET /api/diff", s.auth(s.handleDiff))
	s.mux.HandleFunc("GET /api/rules", s.auth(s.handleRules))
	s.mux.HandleFunc("GET /api/dir", s.auth(s.handleDir))
	s.mux.HandleFunc("GET /api/trend", s.auth(s.handleTrend))
	return s, nil
}

func (s *Server) loadAssets() error {
	entries := []struct{ path, ctype string }{
		{"/index.html", "text/html; charset=utf-8"},
		{"/app.js", "text/javascript; charset=utf-8"},
		{"/app.css", "text/css; charset=utf-8"},
	}
	for _, e := range entries {
		body, err := webFS.ReadFile("web" + e.path)
		if err != nil {
			return fmt.Errorf("embed %s: %w", e.path, err)
		}
		s.assets[e.path] = staticAsset{body: body, ctype: e.ctype}
	}
	return nil
}

// Token returns the token API callers must send in X-FatTrace-Token.
func (s *Server) Token() string { return s.token }

// Handler returns the full middleware-wrapped handler.
func (s *Server) Handler() http.Handler { return withHeaders(s.mux) }

// Serve runs the HTTP server on ln until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// NewToken returns a random 128-bit hex token for one server session.
func NewToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(tokenHeader)
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeError(w, http.StatusUnauthorized, "missing or invalid token")
			return
		}
		next(w, r)
	}
}

func (s *Server) serveAsset(name string) http.HandlerFunc {
	a := s.assets[name]
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", a.ctype)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(a.body))
	}
}

func withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
				"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// listenAddress aliases the listener type so Serve does not depend on net in
// its signature docs; it is always a *net.TCPListener on loopback.
type listenAddress = interface {
	Accept() (c any, err error)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers are already sent; nothing useful left to do.
		return
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

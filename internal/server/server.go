// Package server is the HTTP half of the app: a JSON API over the clips
// directory and the quota policy, and the embedded web client in front of it.
//
// The server holds no state of its own. The clips directory is read fresh on
// every listing (see internal/clips), and the quota config is one JSON file in
// the data directory — so the API answers with the truth on disk, and two
// instances pointed at the same directory cannot disagree.
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chinmay28/clip-manager/internal/clips"
	"github.com/chinmay28/clip-manager/internal/storage"
	"github.com/chinmay28/clip-manager/internal/version"
)

// The built web client, compiled in so the binary is the whole deployment.
// `make build-web` writes this directory; it is committed so a bare `go build`
// still produces a working server.
//
//go:embed all:dist
var distFS embed.FS

// Server serves the API and the embedded client.
type Server struct {
	// ClipsDir is the directory the cameras record into.
	ClipsDir string
	// ConfigPath is where the quota policy lives (JSON).
	ConfigPath string

	// mu serialises config writes and enforcement runs. Listings do not take
	// it — they read the directory, which is safe beside a delete.
	mu sync.Mutex
}

// Handler builds the routing table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/clips", s.handleClips)
	mux.HandleFunc("GET /api/clip", s.handleClip)
	mux.HandleFunc("GET /api/storage", s.handleStorage)
	mux.HandleFunc("PUT /api/storage/config", s.handleConfigPut)
	mux.HandleFunc("POST /api/storage/enforce", s.handleEnforce)
	mux.Handle("/", s.spaHandler())
	return mux
}

// EnforceLoop runs quota enforcement on a schedule, for as long as the server
// lives. It is quiet when nothing is over a line and says exactly what it
// deleted when something is — an unattended job that removes footage in
// silence is not one anybody should trust.
func (s *Server) EnforceLoop(every time.Duration) {
	for {
		time.Sleep(every)
		report, err := s.enforce(false)
		if err != nil {
			log.Printf("quota enforcement: %v", err)
			continue
		}
		if len(report.Deleted) > 0 {
			log.Printf("quota enforcement: deleted %d clip(s), freed %s",
				len(report.Deleted), formatBytes(report.FreedBytes))
		}
		for _, f := range report.Failed {
			log.Printf("quota enforcement: could not remove %s", f)
		}
	}
}

func (s *Server) enforce(dryRun bool) (storage.Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := storage.Load(s.ConfigPath)
	if err != nil {
		return storage.Report{}, err
	}
	return storage.Enforce(s.ClipsDir, cfg, dryRun)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"status":    "ok",
		"version":   version.String(),
		"clips_dir": s.ClipsDir,
	})
}

func (s *Server) handleClips(w http.ResponseWriter, r *http.Request) {
	list, err := clips.Scan(s.ClipsDir)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "listing clips: %v", err)
		return
	}
	writeJSON(w, map[string]any{"clips": list})
}

// handleClip serves one recording's bytes, with range support — that is what
// lets a browser scrub a playable clip rather than downloading it whole.
func (s *Server) handleClip(w http.ResponseWriter, r *http.Request) {
	full, ok := s.resolve(r.URL.Query().Get("path"))
	if !ok {
		httpError(w, http.StatusBadRequest, "bad path")
		return
	}
	// .dav has no registered MIME type; without this the Go file server
	// sniffs it as application/octet-stream, which is right, but naming it
	// keeps a download's filename intact across proxies.
	if strings.EqualFold(filepath.Ext(full), ".dav") {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	http.ServeFile(w, r, full)
}

// resolve turns an API-supplied relative path into an absolute one inside the
// clips directory, refusing anything that would escape it. The API hands out
// these paths itself (clips.Scan), but the check does not rely on that: any
// string arriving over HTTP gets the same treatment.
func (s *Server) resolve(rel string) (string, bool) {
	if rel == "" {
		return "", false
	}
	// Clean with a leading slash so ".." cannot climb, then re-relativise.
	cleaned := path.Clean("/" + filepath.ToSlash(rel))
	full := filepath.Join(s.ClipsDir, filepath.FromSlash(strings.TrimPrefix(cleaned, "/")))
	if rp, err := filepath.Rel(s.ClipsDir, full); err != nil || strings.HasPrefix(rp, "..") {
		return "", false
	}
	return full, true
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	usage, err := storage.Measure(s.ClipsDir)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "measuring: %v", err)
		return
	}
	cfg, err := storage.Load(s.ConfigPath)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading config: %v", err)
		return
	}
	writeJSON(w, map[string]any{"usage": usage, "config": cfg})
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	var cfg storage.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		httpError(w, http.StatusBadRequest, "bad config: %v", err)
		return
	}
	if cfg.QuotaBytes < 0 {
		httpError(w, http.StatusBadRequest, "quota_bytes cannot be negative")
		return
	}
	for name, q := range cfg.CameraQuotaBytes {
		if q < 0 {
			httpError(w, http.StatusBadRequest, "camera %q: quota cannot be negative", name)
			return
		}
	}
	s.mu.Lock()
	err := storage.Save(s.ConfigPath, cfg)
	s.mu.Unlock()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "saving config: %v", err)
		return
	}
	writeJSON(w, map[string]any{"config": cfg})
}

func (s *Server) handleEnforce(w http.ResponseWriter, r *http.Request) {
	dryRun := r.URL.Query().Get("dry_run") == "1"
	report, err := s.enforce(dryRun)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "enforcing: %v", err)
		return
	}
	writeJSON(w, report)
}

// spaHandler serves the embedded client, answering every path that is not a
// file in the bundle with index.html — the client owns its own routing.
func (s *Server) spaHandler() http.Handler {
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err) // the embed directive guarantees dist exists
	}
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" {
			if f, err := dist.Open(p); err == nil {
				f.Close()
				files.ServeHTTP(w, r)
				return
			}
		}
		http.ServeFileFS(w, r, dist, "index.html")
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf(format, args...)})
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

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
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
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
	// PinnedSources are the clip directories named on the command line (or
	// by the systemd unit). They are always in the source set and the API
	// cannot remove them — what the operator pinned at launch outranks what
	// anybody clicks in a browser.
	PinnedSources []string
	// ConfigPath is where the quota policy and the runtime-added sources
	// live (JSON).
	ConfigPath string
	// FFmpeg is the path to an ffmpeg binary, or empty when the machine has
	// none. It is what turns a .dav from a download into something the
	// browser plays: /api/clip/play repackages the recording through it on
	// the fly. Found once at startup — a tool appearing mid-flight is a
	// restart, not a poll.
	FFmpeg string

	// mu serialises config writes and enforcement runs. Listings do not take
	// it — they read the directories, which is safe beside a delete.
	mu sync.Mutex
}

// Handler builds the routing table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/clips", s.handleClips)
	mux.HandleFunc("GET /api/clip", s.handleClip)
	mux.HandleFunc("GET /api/clip/play", s.handleClipPlay)
	mux.HandleFunc("GET /api/sources", s.handleSourcesGet)
	mux.HandleFunc("POST /api/sources", s.handleSourceAdd)
	mux.HandleFunc("DELETE /api/sources", s.handleSourceRemove)
	mux.HandleFunc("GET /api/storage", s.handleStorage)
	mux.HandleFunc("PUT /api/storage/config", s.handleConfigPut)
	mux.HandleFunc("POST /api/storage/enforce", s.handleEnforce)
	mux.Handle("/", s.spaHandler())
	return mux
}

// sources is the effective source set: what the command line pinned, then
// what the config added, deduplicated in that order. Resolved fresh on every
// request — the config is the source of truth and may have just changed.
func (s *Server) sources() ([]string, error) {
	cfg, err := storage.Load(s.ConfigPath)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, src := range append(append([]string{}, s.PinnedSources...), cfg.Sources...) {
		src = filepath.Clean(src)
		if seen[src] {
			continue
		}
		seen[src] = true
		out = append(out, src)
	}
	return out, nil
}

func (s *Server) pinned(src string) bool {
	for _, p := range s.PinnedSources {
		if filepath.Clean(p) == src {
			return true
		}
	}
	return false
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
	srcs, err := s.sources()
	if err != nil {
		return storage.Report{}, err
	}
	return storage.Enforce(srcs, cfg, dryRun)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	srcs, _ := s.sources()
	writeJSON(w, map[string]any{
		"status":  "ok",
		"version": version.String(),
		"sources": srcs,
		"ffmpeg":  s.FFmpeg != "",
	})
}

func (s *Server) handleClips(w http.ResponseWriter, r *http.Request) {
	srcs, err := s.sources()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading sources: %v", err)
		return
	}
	list, missing := clips.ScanAll(srcs)
	if s.FFmpeg != "" {
		for i := range list {
			list[i].Remuxable = clips.RemuxCandidate(list[i].Ext)
		}
	}
	writeJSON(w, map[string]any{"clips": list, "missing_sources": missing})
}

// handleSourcesGet lists the effective source set: path, whether the command
// line pinned it, and whether it is readable right now. Cheap on purpose — no
// walk; the usage figures live on /api/storage.
func (s *Server) handleSourcesGet(w http.ResponseWriter, r *http.Request) {
	srcs, err := s.sources()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading sources: %v", err)
		return
	}
	type row struct {
		Path      string `json:"path"`
		Pinned    bool   `json:"pinned"`
		Available bool   `json:"available"`
	}
	rows := []row{}
	for _, src := range srcs {
		info, statErr := os.Stat(src)
		rows = append(rows, row{
			Path:      src,
			Pinned:    s.pinned(src),
			Available: statErr == nil && info.IsDir(),
		})
	}
	writeJSON(w, map[string]any{"sources": rows})
}

// handleSourceAdd puts one more directory under management. The path has to
// exist and be a directory already: this adopts footage, it does not invent
// places for it, and creating a mistyped path would report "no clips" instead
// of the mistake. Adding a source also puts its footage under the quotas, so
// the bar for accepting a path is "it is really there".
func (s *Server) handleSourceAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "bad request: %v", err)
		return
	}
	src := filepath.Clean(strings.TrimSpace(body.Path))
	if !filepath.IsAbs(src) {
		httpError(w, http.StatusBadRequest, "path must be absolute (got %q)", body.Path)
		return
	}
	info, err := os.Stat(src)
	if err != nil {
		httpError(w, http.StatusBadRequest, "%s does not exist — point at the directory the cameras record into", src)
		return
	}
	if !info.IsDir() {
		httpError(w, http.StatusBadRequest, "%s is not a directory", src)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := storage.Load(s.ConfigPath)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading config: %v", err)
		return
	}
	if s.pinned(src) || sliceHas(cfg.Sources, src) {
		httpError(w, http.StatusConflict, "%s is already a source", src)
		return
	}
	cfg.Sources = append(cfg.Sources, src)
	if err := storage.Save(s.ConfigPath, cfg); err != nil {
		httpError(w, http.StatusInternalServerError, "saving config: %v", err)
		return
	}
	writeJSON(w, map[string]any{"added": src})
}

// handleSourceRemove forgets a runtime-added source. The directory and every
// clip in it are left exactly as they are — removal takes the footage out of
// the app's view and out from under the quotas, it deletes nothing. Pinned
// sources are refused: the command line put them there and only the command
// line takes them away.
func (s *Server) handleSourceRemove(w http.ResponseWriter, r *http.Request) {
	src := filepath.Clean(strings.TrimSpace(r.URL.Query().Get("path")))
	if s.pinned(src) {
		httpError(w, http.StatusBadRequest, "%s is pinned by the command line — remove it from the service's flags instead", src)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := storage.Load(s.ConfigPath)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading config: %v", err)
		return
	}
	if !sliceHas(cfg.Sources, src) {
		httpError(w, http.StatusNotFound, "%s is not a source", src)
		return
	}
	kept := cfg.Sources[:0]
	for _, existing := range cfg.Sources {
		if existing != src {
			kept = append(kept, existing)
		}
	}
	cfg.Sources = kept
	if err := storage.Save(s.ConfigPath, cfg); err != nil {
		httpError(w, http.StatusInternalServerError, "saving config: %v", err)
		return
	}
	writeJSON(w, map[string]any{"removed": src})
}

func sliceHas(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// handleClip serves one recording's bytes, with range support — that is what
// lets a browser scrub a playable clip rather than downloading it whole. The
// clip is addressed the way the listing handed it out: which source, and the
// path relative to it.
func (s *Server) handleClip(w http.ResponseWriter, r *http.Request) {
	full, ok := s.resolve(r.URL.Query().Get("source"), r.URL.Query().Get("path"))
	if !ok {
		httpError(w, http.StatusBadRequest, "bad source or path")
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

// handleClipPlay streams a recording the browser cannot take as-is —
// .dav above all — repackaged on the fly into fragmented MP4. The codec is
// copied, never re-encoded: a Dahua camera's H.264 is already something a
// browser decodes, it is only the container the browser refuses, and a stream
// copy is cheap enough for a Raspberry Pi where a transcode is not.
//
// The output has no known length, so there is no Content-Length and no range
// support — the player streams from the start rather than scrubbing freely.
// The trade is deliberate: seekable output would mean remuxing the whole file
// to disk first, and a clip a few seconds long does not earn that.
//
// ffmpeg's first bytes are awaited before the response status is committed,
// because failure here is ordinary — an .avi carrying MJPEG, a truncated
// file — and it must arrive as an error the client can read, not a 200 that
// dies mid-body.
func (s *Server) handleClipPlay(w http.ResponseWriter, r *http.Request) {
	if s.FFmpeg == "" {
		httpError(w, http.StatusNotImplemented, "playing this format needs ffmpeg on the server, and none was found — install it and restart, or download the clip instead")
		return
	}
	full, ok := s.resolve(r.URL.Query().Get("source"), r.URL.Query().Get("path"))
	if !ok {
		httpError(w, http.StatusBadRequest, "bad source or path")
		return
	}

	// CommandContext ties ffmpeg's life to the request: a closed tab kills
	// the process rather than leaving it piping into nothing.
	cmd := exec.CommandContext(r.Context(), s.FFmpeg,
		"-v", "error",
		"-i", full,
		"-c", "copy",
		// Fragmented MP4 is what makes an unseekable pipe playable at all:
		// a plain MP4 puts its index at the end, which a stream never
		// reaches until it is over.
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4", "pipe:1",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "starting ffmpeg: %v", err)
		return
	}
	if err := cmd.Start(); err != nil {
		httpError(w, http.StatusInternalServerError, "starting ffmpeg: %v", err)
		return
	}
	defer cmd.Wait()

	// The probe read: EOF before a single byte means ffmpeg gave up on the
	// input, and whatever it said on stderr is the only useful answer.
	first := make([]byte, 64*1024)
	n, readErr := io.ReadAtLeast(stdout, first, 1)
	if n == 0 {
		cmd.Wait()
		msg := strings.TrimSpace(stderr.String())
		if msg == "" && readErr != nil {
			msg = readErr.Error()
		}
		httpError(w, http.StatusUnprocessableEntity, "ffmpeg could not repackage this clip: %s", msg)
		return
	}

	w.Header().Set("Content-Type", "video/mp4")
	if _, err := w.Write(first[:n]); err != nil {
		return // the client went away; the deferred Wait reaps ffmpeg
	}
	io.Copy(w, stdout)
}

// resolve turns an API-supplied (source, relative path) pair into an absolute
// path, refusing a source that is not in the configured set and a path that
// would escape it. The API hands out both halves itself (clips.ScanAll), but
// the check does not rely on that: any string arriving over HTTP gets the
// same treatment — the source must match a configured root exactly, so the
// endpoint can serve nothing the source list does not already admit to.
func (s *Server) resolve(source, rel string) (string, bool) {
	if source == "" || rel == "" {
		return "", false
	}
	srcs, err := s.sources()
	if err != nil {
		return "", false
	}
	source = filepath.Clean(source)
	if !sliceHas(srcs, source) {
		return "", false
	}
	// Clean with a leading slash so ".." cannot climb, then re-relativise.
	cleaned := path.Clean("/" + filepath.ToSlash(rel))
	full := filepath.Join(source, filepath.FromSlash(strings.TrimPrefix(cleaned, "/")))
	if rp, err := filepath.Rel(source, full); err != nil || strings.HasPrefix(rp, "..") {
		return "", false
	}
	return full, true
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	srcs, err := s.sources()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading sources: %v", err)
		return
	}
	cfg, err := storage.Load(s.ConfigPath)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading config: %v", err)
		return
	}
	writeJSON(w, map[string]any{"usage": storage.Measure(srcs), "config": cfg})
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
	defer s.mu.Unlock()
	// This endpoint edits the quotas and nothing else. Sources have their
	// own endpoints — a quota save must not silently drop a source somebody
	// added between this client's load and its save.
	current, err := storage.Load(s.ConfigPath)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading config: %v", err)
		return
	}
	cfg.Sources = current.Sources
	if err := storage.Save(s.ConfigPath, cfg); err != nil {
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

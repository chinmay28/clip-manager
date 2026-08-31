// Package server is the HTTP half of the app: a JSON API over the clips
// directory and the quota policy, and the embedded web client in front of it.
//
// The server holds no state of its own. The clips directory is read fresh on
// every listing (see internal/clips), and the quota config is one JSON file in
// the data directory — so the API answers with the truth on disk, and two
// instances pointed at the same directory cannot disagree.
package server

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
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
	// browser plays: /api/clip/play repackages the recording through it into
	// a cached MP4. Found once at startup — a tool appearing mid-flight is a
	// restart, not a poll.
	FFmpeg string

	// mu serialises config writes and enforcement runs. Listings do not take
	// it — they read the directories, which is safe beside a delete.
	mu sync.Mutex

	// playMu guards playLocks, the per-clip locks that keep two requests for
	// the same recording from running two ffmpegs over it.
	playMu    sync.Mutex
	playLocks map[string]*sync.Mutex
}

// Handler builds the routing table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/clips", s.handleClips)
	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("GET /api/clip", s.handleClip)
	mux.HandleFunc("GET /api/clip/play", s.handleClipPlay)
	mux.HandleFunc("GET /api/sources", s.handleSourcesGet)
	mux.HandleFunc("POST /api/sources", s.handleSourceAdd)
	mux.HandleFunc("DELETE /api/sources", s.handleSourceRemove)
	mux.HandleFunc("PUT /api/channels/label", s.handleChannelLabel)
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

// dayOf buckets a clip by the calendar day its recording started, in the
// server's local zone — the same zone the timestamps were parsed in.
func dayOf(c clips.Clip) string { return c.StartTime.Local().Format("2006-01-02") }

// handleClips lists clips — all of them, or one day of one channel. The
// filters exist because the volume demands them: cameras write hundreds of
// clips a day, and a client that only wants Tuesday's front door footage
// should not be handed the entire archive to throw most of it away.
//
//	?day=2026-08-30   only clips whose recording started that day (local time)
//	?channel=X        only that channel ("" is a real channel: the clips
//	                  nothing claims — presence of the parameter is what
//	                  filters, so it must be Has, not a zero check)
func (s *Server) handleClips(w http.ResponseWriter, r *http.Request) {
	srcs, err := s.sources()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading sources: %v", err)
		return
	}
	list, missing := clips.ScanAll(srcs)

	q := r.URL.Query()
	if day := q.Get("day"); day != "" {
		if _, err := time.ParseInLocation("2006-01-02", day, time.Local); err != nil {
			httpError(w, http.StatusBadRequest, "day must be YYYY-MM-DD (got %q)", day)
			return
		}
		list = filterClips(list, func(c clips.Clip) bool { return dayOf(c) == day })
	}
	if q.Has("channel") {
		channel := q.Get("channel")
		list = filterClips(list, func(c clips.Clip) bool { return c.Channel == channel })
	}

	if s.FFmpeg != "" {
		for i := range list {
			list[i].Remuxable = clips.RemuxCandidate(list[i].Ext)
		}
	}
	// The channel labels ride along with the clips because they are read
	// together every time: a listing without its labels would flash raw
	// channel keys at the user on every load.
	cfg, err := storage.Load(s.ConfigPath)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading config: %v", err)
		return
	}
	labels := cfg.ChannelLabels
	if labels == nil {
		labels = map[string]string{}
	}
	writeJSON(w, map[string]any{"clips": list, "missing_sources": missing, "channel_labels": labels})
}

func filterClips(list []clips.Clip, keep func(clips.Clip) bool) []clips.Clip {
	out := list[:0]
	for _, c := range list {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

// handleSummary is the shape of the archive without its weight: how many
// clips each channel holds, and — for one channel or all of them — how many
// each day holds, newest day first. It is what the client navigates by; the
// clips themselves come one day at a time from /api/clips. At hundreds of
// clips a day the full listing runs to megabytes, and a phone opening the app
// to check one camera should not download the archive to draw a menu.
//
//	?channel=X   day counts for that channel only (channel totals stay global
//	             — the chips keep their numbers while one is selected)
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	srcs, err := s.sources()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading sources: %v", err)
		return
	}
	list, missing := clips.ScanAll(srcs)

	type bucket struct {
		Clips int   `json:"clips"`
		Bytes int64 `json:"bytes"`
	}
	channels := map[string]*bucket{}
	for _, c := range list {
		b := channels[c.Channel]
		if b == nil {
			b = &bucket{}
			channels[c.Channel] = b
		}
		b.Clips++
		b.Bytes += c.Size
	}

	q := r.URL.Query()
	if q.Has("channel") {
		channel := q.Get("channel")
		list = filterClips(list, func(c clips.Clip) bool { return c.Channel == channel })
	}
	type dayRow struct {
		Day   string `json:"day"`
		Clips int    `json:"clips"`
		Bytes int64  `json:"bytes"`
	}
	byDay := map[string]*dayRow{}
	for _, c := range list {
		day := dayOf(c)
		row := byDay[day]
		if row == nil {
			row = &dayRow{Day: day}
			byDay[day] = row
		}
		row.Clips++
		row.Bytes += c.Size
	}
	days := make([]dayRow, 0, len(byDay))
	for _, row := range byDay {
		days = append(days, *row)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Day > days[j].Day })

	cfg, err := storage.Load(s.ConfigPath)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading config: %v", err)
		return
	}
	labels := cfg.ChannelLabels
	if labels == nil {
		labels = map[string]string{}
	}
	writeJSON(w, map[string]any{
		"channels":        channels,
		"days":            days,
		"channel_labels":  labels,
		"missing_sources": missing,
	})
}

// handleChannelLabel names (or, with an empty label, un-names) one channel.
// Labels are cosmetic — nothing enforces against them — so this endpoint edits
// exactly one key and leaves the rest of the config alone.
func (s *Server) handleChannelLabel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Channel string `json:"channel"`
		Label   string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "bad request: %v", err)
		return
	}
	channel := strings.TrimSpace(body.Channel)
	if channel == "" {
		httpError(w, http.StatusBadRequest, "channel is required")
		return
	}
	label := strings.TrimSpace(body.Label)

	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := storage.Load(s.ConfigPath)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading config: %v", err)
		return
	}
	if label == "" {
		delete(cfg.ChannelLabels, channel)
	} else {
		if cfg.ChannelLabels == nil {
			cfg.ChannelLabels = map[string]string{}
		}
		cfg.ChannelLabels[channel] = label
	}
	if err := storage.Save(s.ConfigPath, cfg); err != nil {
		httpError(w, http.StatusInternalServerError, "saving config: %v", err)
		return
	}
	writeJSON(w, map[string]any{"channel": channel, "label": label})
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

// handleClipPlay serves a recording the browser cannot take as-is — .dav
// above all — as a real MP4 file, prepared once through ffmpeg and cached.
//
// It used to stream fragmented MP4 straight out of ffmpeg's stdout, which
// desktop Chrome played and iOS Safari flatly refused: Safari insists on
// range requests against a resource with a known length. So the recording is
// repackaged into a complete `+faststart` MP4 on disk first and served with
// http.ServeFile, which gives every browser the ranges, the length, and the
// scrubbing the old stream never had. A security clip is seconds long; the
// remux is subsecond and paid once per clip, not once per view.
//
// What ffmpeg does to the video stream depends on what is in it:
//
//   - H.264 → container copy. Every browser decodes it.
//   - HEVC  → container copy, tagged hvc1 — the tag Safari requires before it
//     will even try (ffmpeg's default hev1 reads as undecodable there).
//   - anything else (MJPEG in an old .avi, MPEG-4 part 2…) → transcode to
//     H.264, because no browser will ever decode it natively.
//
// `?transcode=1` forces the H.264 transcode: the player's fallback for a
// browser that cannot decode the copied codec (HEVC on Firefox, say). And a
// copy that fails mid-remux falls back to transcoding on its own — the goal
// is that Play plays, not that the cheap path was tried.
func (s *Server) handleClipPlay(w http.ResponseWriter, r *http.Request) {
	full, ok := s.resolve(r.URL.Query().Get("source"), r.URL.Query().Get("path"))
	if !ok {
		httpError(w, http.StatusBadRequest, "bad source or path")
		return
	}
	if s.FFmpeg == "" {
		httpError(w, http.StatusNotImplemented, "playing this format needs ffmpeg on the server, and none was found — install it and restart, or download the clip instead")
		return
	}
	info, err := os.Stat(full)
	if err != nil {
		httpError(w, http.StatusNotFound, "clip not found")
		return
	}
	mode := "auto"
	if r.URL.Query().Get("transcode") == "1" {
		mode = "transcode"
	}

	// The cache key is the clip's identity AND its content (size, mtime): a
	// camera overwriting a file yields a fresh entry, not a stale replay.
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%s", full, info.Size(), info.ModTime().UnixNano(), mode)))
	key := hex.EncodeToString(sum[:12])
	cached := filepath.Join(s.playCacheDir(), key+".mp4")

	if _, err := os.Stat(cached); err != nil {
		unlock := s.lockPlay(key)
		// Re-check under the lock: the request holding it before us may have
		// prepared exactly this file.
		if _, err := os.Stat(cached); err != nil {
			if err := s.preparePlay(r.Context(), full, cached, mode); err != nil {
				unlock()
				httpError(w, http.StatusUnprocessableEntity, "ffmpeg could not repackage this clip: %v", err)
				return
			}
			s.prunePlayCache()
		}
		unlock()
	}

	w.Header().Set("Content-Type", "video/mp4")
	http.ServeFile(w, r, cached)
}

func (s *Server) playCacheDir() string {
	return filepath.Join(filepath.Dir(s.ConfigPath), "playcache")
}

// lockPlay takes the per-clip preparation lock, so two tabs asking for the
// same recording run one ffmpeg between them. The returned func releases it.
func (s *Server) lockPlay(key string) func() {
	s.playMu.Lock()
	if s.playLocks == nil {
		s.playLocks = map[string]*sync.Mutex{}
	}
	mu := s.playLocks[key]
	if mu == nil {
		mu = &sync.Mutex{}
		s.playLocks[key] = mu
	}
	s.playMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// videoCodecRe reads the video codec out of ffmpeg's own description of an
// input — `Stream #0:0: Video: h264 (Main), yuv420p…` → "h264". ffprobe would
// be the purpose-built tool, but ffmpeg is the one binary this app requires
// and its banner carries the same answer.
var videoCodecRe = regexp.MustCompile(`Video: ([A-Za-z0-9_]+)`)

func (s *Server) probeVideoCodec(ctx context.Context, in string) (string, error) {
	// With no output file ffmpeg prints the input description and exits
	// non-zero; the exit code is noise, the description is the product.
	out, _ := exec.CommandContext(ctx, s.FFmpeg, "-hide_banner", "-i", in).CombinedOutput()
	if m := videoCodecRe.FindSubmatch(out); m != nil {
		return strings.ToLower(string(m[1])), nil
	}
	msg := strings.TrimSpace(string(out))
	if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
		msg = strings.TrimSpace(msg[i+1:]) // the last line is ffmpeg's verdict
	}
	if msg == "" {
		msg = "no video stream found"
	}
	return "", fmt.Errorf("%s", msg)
}

// preparePlay writes the browser-ready MP4 for one recording: probe, copy or
// transcode per the rules on handleClipPlay, into a temp file renamed over
// the cache path only when ffmpeg succeeded — a half-written MP4 must never
// be servable.
func (s *Server) preparePlay(ctx context.Context, in, cached, mode string) error {
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		return err
	}
	transcodeArgs := []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p"}
	if mode == "transcode" {
		return s.runFFmpeg(ctx, in, cached, transcodeArgs)
	}
	codec, err := s.probeVideoCodec(ctx, in)
	if err != nil {
		return err
	}
	var copyArgs []string
	switch codec {
	case "h264":
		copyArgs = []string{"-c:v", "copy"}
	case "hevc", "h265":
		copyArgs = []string{"-c:v", "copy", "-tag:v", "hvc1"}
	}
	if copyArgs != nil {
		if err := s.runFFmpeg(ctx, in, cached, copyArgs); err == nil {
			return nil
		}
		// The copy failed on a codec that should have copied — a damaged
		// stream, usually. The transcode below decodes around much of that.
	}
	return s.runFFmpeg(ctx, in, cached, transcodeArgs)
}

func (s *Server) runFFmpeg(ctx context.Context, in, out string, videoArgs []string) error {
	// Video stream 0 and the first audio stream if any; data and subtitle
	// streams (Dahua files carry both) never survive into the MP4. Audio is
	// re-encoded to AAC unconditionally — camera audio is G.711 more often
	// than not, which no browser plays inside an MP4.
	args := []string{"-v", "error", "-y", "-i", in,
		"-map", "0:v:0", "-map", "0:a:0?", "-dn", "-sn"}
	args = append(args, videoArgs...)
	args = append(args, "-c:a", "aac", "-movflags", "+faststart", "-f", "mp4")
	tmp := fmt.Sprintf("%s.tmp%d", out, os.Getpid())
	args = append(args, tmp)

	cmd := exec.CommandContext(ctx, s.FFmpeg, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return os.Rename(tmp, out)
}

// playCacheCap bounds the prepared-MP4 cache. Half a gigabyte holds days of
// short clips; past it the least recently touched go first. The cache is a
// convenience, never the record — every entry can be rebuilt from the
// recording it came from.
const playCacheCap = 512 << 20

func (s *Server) prunePlayCache() {
	dir := s.playCacheDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		name string
		size int64
		at   time.Time
	}
	var files []file
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".mp4") {
			// A .tmp still warm may belong to a running ffmpeg; one gone
			// cold is a crashed run's leavings.
			if time.Since(info.ModTime()) > time.Hour {
				os.Remove(filepath.Join(dir, e.Name()))
			}
			continue
		}
		files = append(files, file{e.Name(), info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.Before(files[j].at) })
	for _, f := range files {
		if total <= playCacheCap {
			break
		}
		if os.Remove(filepath.Join(dir, f.name)) == nil {
			total -= f.size
		}
	}
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
	for name, q := range cfg.ChannelQuotaBytes {
		if q < 0 {
			httpError(w, http.StatusBadRequest, "channel %q: quota cannot be negative", name)
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// This endpoint edits the quotas and nothing else. Sources and channel
	// labels have their own endpoints — a quota save must not silently drop
	// a source or a label somebody set between this client's load and its
	// save.
	current, err := storage.Load(s.ConfigPath)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "reading config: %v", err)
		return
	}
	cfg.Sources = current.Sources
	cfg.ChannelLabels = current.ChannelLabels
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

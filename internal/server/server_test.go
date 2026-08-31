package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testServer(t *testing.T, source string) *Server {
	t.Helper()
	ffmpeg, _ := exec.LookPath("ffmpeg")
	return &Server{
		PinnedSources: []string{source},
		ConfigPath:    filepath.Join(t.TempDir(), "config.json"),
		FFmpeg:        ffmpeg,
	}
}

// needFFmpeg generates a two-second H.264 test recording in a container a
// browser will not take directly, or skips the test on a machine with no
// ffmpeg — the remux path is defined as "what the machine's ffmpeg can do",
// so without one there is nothing to assert.
func needFFmpeg(t *testing.T, dir, name string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on this machine")
	}
	out := filepath.Join(dir, name)
	cmd := exec.Command(ffmpeg, "-v", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=128x96:rate=10",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("this ffmpeg cannot make a test clip: %v: %s", err, msg)
	}
}

func TestPlayRemuxesToFragmentedMP4(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "cam"), 0o755); err != nil {
		t.Fatal(err)
	}
	needFFmpeg(t, filepath.Join(source, "cam"), "clip.ts")
	s := testServer(t, source)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/clip/play?source="+source+"&path=cam/clip.ts", nil)
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("want video/mp4, got %q", ct)
	}
	// An MP4 leads with an ftyp box within its first bytes; its absence
	// means whatever streamed back was not the remux.
	if !bytes.Contains(rec.Body.Bytes()[:64], []byte("ftyp")) {
		t.Fatal("response does not start with an MP4 ftyp box")
	}
}

func TestPlayReportsAnUnreadableFileAsAnError(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "cam"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Junk wearing the .dav extension: ffmpeg must fail, and the failure
	// must come back as a readable error, never a 200 that dies mid-body.
	if err := os.WriteFile(filepath.Join(source, "cam", "junk.dav"), []byte("not a recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, source)
	if s.FFmpeg == "" {
		t.Skip("no ffmpeg on this machine")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/clip/play?source="+source+"&path=cam/junk.dav", nil)
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != 422 {
		t.Fatalf("want 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPlayRefusesAnUnlistedSource(t *testing.T) {
	s := testServer(t, t.TempDir())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/clip/play?source=/etc&path=passwd", nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("an unlisted source must be refused: got %d", rec.Code)
	}
}

func TestPlayWithoutFFmpegSaysSo(t *testing.T) {
	source := t.TempDir()
	s := testServer(t, source)
	s.FFmpeg = ""

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/clip/play?source="+source+"&path=cam/a.dav", nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 501 {
		t.Fatalf("want 501 naming the missing tool, got %d", rec.Code)
	}
}

func TestChannelLabelRoundTrip(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "N843A8_ch3_main_20260830214636_20260830214641.dav"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, source)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/channels/label",
		strings.NewReader(`{"channel":"N843A8_ch3","label":"Front door"}`))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("label save: want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// The listing carries the labels back, and the clip carries its channel.
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/clips", nil))
	var body struct {
		Clips         []map[string]any  `json:"clips"`
		ChannelLabels map[string]string `json:"channel_labels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ChannelLabels["N843A8_ch3"] != "Front door" {
		t.Fatalf("labels did not round-trip: %v", body.ChannelLabels)
	}
	if len(body.Clips) != 1 || body.Clips[0]["channel"] != "N843A8_ch3" {
		t.Fatalf("clip channel not parsed: %v", body.Clips)
	}

	// A quota save must not wipe the label (it edits quotas, nothing else).
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("PUT", "/api/storage/config", strings.NewReader(`{"quota_bytes":1024}`))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("config save: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/clips", nil))
	body.ChannelLabels = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ChannelLabels["N843A8_ch3"] != "Front door" {
		t.Fatal("a quota save wiped the channel labels")
	}

	// An empty label forgets the channel's name.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("PUT", "/api/channels/label",
		strings.NewReader(`{"channel":"N843A8_ch3","label":""}`))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("label clear: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

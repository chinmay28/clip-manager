package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeClip creates a fake recording of a known size and age. Ages are spread
// out so the oldest-first order is unambiguous.
func writeClip(t *testing.T, root, rel string, size int, ageMinutes int) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := time.Now().Add(-time.Duration(ageMinutes) * time.Minute)
	if err := os.Chtimes(full, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

func TestEnforceDeletesOldestFirstUnderGlobalQuota(t *testing.T) {
	root := t.TempDir()
	writeClip(t, root, "cam1/old.dav", 100, 30)
	writeClip(t, root, "cam1/mid.dav", 100, 20)
	writeClip(t, root, "cam2/new.dav", 100, 10)

	report, err := Enforce(root, Config{QuotaBytes: 250}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Deleted) != 1 || report.Deleted[0].Path != "cam1/old.dav" {
		t.Fatalf("want exactly cam1/old.dav deleted, got %+v", report.Deleted)
	}
	if exists(root, "cam1/old.dav") || !exists(root, "cam1/mid.dav") || !exists(root, "cam2/new.dav") {
		t.Fatal("wrong files removed from disk")
	}
}

func TestEnforceCameraQuotaSparesOtherCameras(t *testing.T) {
	root := t.TempDir()
	writeClip(t, root, "noisy/a.dav", 100, 40)
	writeClip(t, root, "noisy/b.dav", 100, 30)
	writeClip(t, root, "quiet/old.dav", 100, 50) // oldest of all, but under its own line

	cfg := Config{CameraQuotaBytes: map[string]int64{"noisy": 100}}
	report, err := Enforce(root, cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Deleted) != 1 || report.Deleted[0].Path != "noisy/a.dav" || report.Deleted[0].Rule != "camera" {
		t.Fatalf("want noisy/a.dav via the camera rule, got %+v", report.Deleted)
	}
	if !exists(root, "quiet/old.dav") {
		t.Fatal("a camera quota deleted another camera's footage")
	}
}

func TestEnforceDryRunTouchesNothing(t *testing.T) {
	root := t.TempDir()
	writeClip(t, root, "cam/a.dav", 100, 20)
	writeClip(t, root, "cam/b.dav", 100, 10)

	report, err := Enforce(root, Config{QuotaBytes: 100}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Deleted) != 1 || !report.DryRun {
		t.Fatalf("dry run should report one candidate, got %+v", report)
	}
	if !exists(root, "cam/a.dav") || !exists(root, "cam/b.dav") {
		t.Fatal("dry run removed a file")
	}
}

func TestEnforceNeverTouchesForeignFiles(t *testing.T) {
	root := t.TempDir()
	writeClip(t, root, "cam/a.dav", 100, 20)
	// Not footage: must be invisible to the quota and untouchable, however
	// far over the line the directory is.
	writeClip(t, root, "cam/notes.txt", 10_000, 90)

	report, err := Enforce(root, Config{QuotaBytes: 50}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !exists(root, "cam/notes.txt") {
		t.Fatal("enforcement deleted a non-clip file")
	}
	if len(report.Deleted) != 1 || report.Deleted[0].Path != "cam/a.dav" {
		t.Fatalf("want only cam/a.dav gone, got %+v", report.Deleted)
	}
}

func TestEnforceNoQuotaIsANoOp(t *testing.T) {
	root := t.TempDir()
	writeClip(t, root, "cam/a.dav", 1000, 10)

	report, err := Enforce(root, Config{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Deleted) != 0 || !exists(root, "cam/a.dav") {
		t.Fatal("enforcement acted with no quota configured")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	// Missing file reads as an empty policy, not an error.
	cfg, err := Load(path)
	if err != nil || cfg.QuotaBytes != 0 {
		t.Fatalf("missing config: got %+v, %v", cfg, err)
	}

	want := Config{QuotaBytes: 1 << 30, CameraQuotaBytes: map[string]int64{"front": 1 << 20}}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.QuotaBytes != want.QuotaBytes || got.CameraQuotaBytes["front"] != want.CameraQuotaBytes["front"] {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

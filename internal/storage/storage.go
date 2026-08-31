// Package storage is the half of the app that manages the disk rather than
// showing it: how much the clips directory holds, the quotas drawn over it,
// and the enforcement that deletes the oldest footage when a quota is crossed.
//
// Two kinds of line can be drawn:
//
//   - a global quota — how much the whole clips directory may hold
//   - a per-camera quota — how much one camera's subdirectory may hold
//
// Enforcement deletes oldest-first, camera quotas before the global one, and
// only files whose extension internal/clips recognises as footage — a config
// file, a thumbnail cache or anything else living in the directory is never
// eligible. Deleting recordings unattended is the most dangerous thing this
// app does, so every run produces a report of exactly what went and why, and a
// dry run answers "what would go" without touching anything.
package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chinmay28/clip-manager/internal/clips"
)

// Config is the quota policy, stored as JSON in the data directory. Zero (or a
// missing key) means "no line drawn" — a fresh install enforces nothing until
// somebody asks it to.
type Config struct {
	// QuotaBytes caps the whole clips directory. 0 = unlimited.
	QuotaBytes int64 `json:"quota_bytes"`
	// CameraQuotaBytes caps individual cameras, keyed by the camera's
	// directory name. 0 or absent = unlimited for that camera.
	CameraQuotaBytes map[string]int64 `json:"camera_quota_bytes,omitempty"`
}

// Load reads the config, returning an empty policy when the file does not
// exist yet — no config is a valid state, not an error.
func Load(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Save writes the config atomically — temp file, then rename — so a crash
// mid-write can never leave a half-written policy that enforcement would then
// misread.
func Save(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// CameraUsage is one camera's share of the directory.
type CameraUsage struct {
	Bytes int64 `json:"bytes"`
	Files int   `json:"files"`
}

// Usage is what the clips directory holds, in total and per camera.
type Usage struct {
	Bytes   int64                  `json:"bytes"`
	Files   int                    `json:"files"`
	Cameras map[string]CameraUsage `json:"cameras"`
}

// Measure totals the clips under root. It counts what internal/clips counts —
// footage — so the figure is the one quotas are compared against, not a disk
// `du` that would include foreign files enforcement will never delete.
func Measure(root string) (Usage, error) {
	list, err := clips.Scan(root)
	if err != nil {
		return Usage{}, err
	}
	u := Usage{Cameras: map[string]CameraUsage{}}
	for _, c := range list {
		u.Bytes += c.Size
		u.Files++
		cu := u.Cameras[c.Camera]
		cu.Bytes += c.Size
		cu.Files++
		u.Cameras[c.Camera] = cu
	}
	return u, nil
}

// Deleted is one file enforcement removed (or, on a dry run, would remove),
// and which line it crossed.
type Deleted struct {
	Path   string `json:"path"`
	Camera string `json:"camera"`
	Size   int64  `json:"size"`
	// Rule is "camera" when a per-camera quota claimed the file, "global"
	// when the directory-wide one did.
	Rule string `json:"rule"`
}

// Report is what one enforcement run did, dry or real.
type Report struct {
	DryRun     bool      `json:"dry_run"`
	Deleted    []Deleted `json:"deleted"`
	FreedBytes int64     `json:"freed_bytes"`
	// Failed lists files that should have gone but could not be removed —
	// a read-only mount, a permissions problem. Enforcement carries on past
	// them; their bytes stay counted against the quota.
	Failed []string `json:"failed,omitempty"`
}

// Enforce brings the clips directory back under its quotas by deleting the
// oldest footage first. Camera quotas run before the global one, so a camera
// over its own line pays for itself before well-behaved cameras lose anything
// to the directory-wide total.
//
// With dryRun set nothing is removed; the report says what a real run would
// have done. With no quota configured at all the report is empty — enforcement
// never invents a line nobody drew.
func Enforce(root string, cfg Config, dryRun bool) (Report, error) {
	report := Report{DryRun: dryRun, Deleted: []Deleted{}}
	if cfg.QuotaBytes <= 0 && len(cfg.CameraQuotaBytes) == 0 {
		return report, nil
	}

	list, err := clips.Scan(root) // oldest first — the order deletion consumes
	if err != nil {
		return report, err
	}

	total := int64(0)
	perCamera := map[string]int64{}
	for _, c := range list {
		total += c.Size
		perCamera[c.Camera] += c.Size
	}
	gone := map[string]bool{}

	remove := func(c clips.Clip, rule string) {
		if !dryRun {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(c.Path))); err != nil {
				report.Failed = append(report.Failed, c.Path)
				return
			}
		}
		gone[c.Path] = true
		total -= c.Size
		perCamera[c.Camera] -= c.Size
		report.FreedBytes += c.Size
		report.Deleted = append(report.Deleted, Deleted{
			Path: c.Path, Camera: c.Camera, Size: c.Size, Rule: rule,
		})
	}

	// Per-camera lines first, oldest first within each camera.
	for _, c := range list {
		quota := cfg.CameraQuotaBytes[c.Camera]
		if quota <= 0 || perCamera[c.Camera] <= quota {
			continue
		}
		remove(c, "camera")
	}

	// Then the global line, over whatever is left.
	if cfg.QuotaBytes > 0 {
		for _, c := range list {
			if total <= cfg.QuotaBytes {
				break
			}
			if gone[c.Path] {
				continue
			}
			remove(c, "global")
		}
	}
	return report, nil
}

// Package storage is the half of the app that manages the disk rather than
// showing it: how much the clips directory holds, the quotas drawn over it,
// and the enforcement that deletes the oldest footage when a quota is crossed.
//
// Two kinds of line can be drawn:
//
//   - a global quota — how much the whole clips directory may hold
//   - a per-channel quota — how much one channel's footage may hold
//
// Enforcement deletes oldest-first, channel quotas before the global one, and
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
	// QuotaBytes caps the footage total across every source. 0 = unlimited.
	QuotaBytes int64 `json:"quota_bytes"`
	// ChannelQuotaBytes caps individual channels, keyed by the channel
	// identity internal/clips reads from the recordings themselves
	// ("N843A8_ch3"), with the camera directory as the fallback for files
	// whose names carry no channel token. 0 or absent = unlimited for that
	// channel. The channel is the identity, deliberately: it survives FTP
	// layouts that bucket files by date instead of by camera, and the same
	// channel under two sources is one channel to its quota, so footage
	// split across a disk and its overflow still answers to one line.
	//
	// Quotas key on the channel, never on its user-given label — a label
	// can be set, changed or dropped without moving any line.
	ChannelQuotaBytes map[string]int64 `json:"channel_quota_bytes,omitempty"`
	// LegacyCameraQuotaBytes is the quota's old shape — keyed by the camera
	// directory, before channels existed. Load folds it into
	// ChannelQuotaBytes (for one-directory-per-camera layouts the directory
	// name IS the channel fallback, so the line lands where it always was)
	// and clears it, so a save writes only the new key.
	LegacyCameraQuotaBytes map[string]int64 `json:"camera_quota_bytes,omitempty"`
	// Sources are the clip directories added at runtime (through the app),
	// alongside whatever the command line pinned. Absolute paths.
	Sources []string `json:"sources,omitempty"`
	// ChannelLabels are the names people gave their channels, keyed by the
	// channel identity internal/clips reads from the recordings
	// ("N843A8_ch3"). Purely cosmetic — quotas and enforcement never look at
	// them — so a label can be set, changed or dropped without consequence.
	ChannelLabels map[string]string `json:"channel_labels,omitempty"`
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
	// A config written before quotas moved to channels carries
	// camera_quota_bytes. Fold it in rather than dropping it: a quota
	// someone drew must never silently stop enforcing across an upgrade.
	for name, q := range cfg.LegacyCameraQuotaBytes {
		if cfg.ChannelQuotaBytes == nil {
			cfg.ChannelQuotaBytes = map[string]int64{}
		}
		if _, drawn := cfg.ChannelQuotaBytes[name]; !drawn {
			cfg.ChannelQuotaBytes[name] = q
		}
	}
	cfg.LegacyCameraQuotaBytes = nil
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

// Subtotal is one bucket's share of the directory — a channel's, or a
// source's.
type Subtotal struct {
	Bytes int64 `json:"bytes"`
	Files int   `json:"files"`
}

// Usage is what the sources hold, in total, per channel, and per source.
type Usage struct {
	Bytes    int64               `json:"bytes"`
	Files    int                 `json:"files"`
	Channels map[string]Subtotal `json:"channels"`
	Sources  map[string]Subtotal `json:"sources"`
	// Missing names the sources that could not be read at all — an
	// unplugged drive, a NAS that is down. Their footage is not in the
	// figures above, which is worth saying out loud: a total that silently
	// halves because a disk went away reads as deleted footage.
	Missing []string `json:"missing,omitempty"`
}

// Measure totals a listing of the sources' clips — the walk's own output
// (clips.ScanAll or the scan cache's Listing), which is what keeps the
// figure the one quotas are compared against: footage, not a disk `du` that
// would include foreign files enforcement will never delete.
func Measure(sources []string, list []clips.Clip, missing []string) Usage {
	u := Usage{
		Channels: map[string]Subtotal{},
		Sources:  map[string]Subtotal{},
		Missing:  missing,
	}
	for _, src := range sources {
		if !contains(missing, src) {
			u.Sources[src] = Subtotal{} // a readable, empty source is a row, not an absence
		}
	}
	for _, c := range list {
		u.Bytes += c.Size
		u.Files++
		cu := u.Channels[c.Channel]
		cu.Bytes += c.Size
		cu.Files++
		u.Channels[c.Channel] = cu
		su := u.Sources[c.Source]
		su.Bytes += c.Size
		su.Files++
		u.Sources[c.Source] = su
	}
	return u
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// Deleted is one file enforcement removed (or, on a dry run, would remove),
// and which line it crossed.
type Deleted struct {
	Source  string `json:"source"`
	Path    string `json:"path"`
	Channel string `json:"channel"`
	Size    int64  `json:"size"`
	// Rule is "channel" when a per-channel quota claimed the file, "global"
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

// Enforce brings the sources back under the quotas by deleting the oldest
// footage first — across every source, so the oldest goes first wherever it
// sits. Channel quotas run before the global one, so a channel over its own
// line pays for itself before well-behaved channels lose anything to the
// shared total.
//
// A source that cannot be read contributes nothing to the totals and nothing
// can be deleted from it — which errs the right way: an unplugged drive must
// not make the global figure look over quota, and enforcement must never act
// on footage it cannot see.
//
// With dryRun set nothing is removed; the report says what a real run would
// have done. With no quota configured at all the report is empty — enforcement
// never invents a line nobody drew.
func Enforce(sources []string, cfg Config, dryRun bool) (Report, error) {
	report := Report{DryRun: dryRun, Deleted: []Deleted{}}
	if cfg.QuotaBytes <= 0 && len(cfg.ChannelQuotaBytes) == 0 {
		return report, nil
	}

	list, _ := clips.ScanAll(sources) // oldest first — the order deletion consumes

	total := int64(0)
	perChannel := map[string]int64{}
	for _, c := range list {
		total += c.Size
		perChannel[c.Channel] += c.Size
	}
	gone := map[string]bool{}
	key := func(c clips.Clip) string { return c.Source + "\x00" + c.Path }

	remove := func(c clips.Clip, rule string) {
		if !dryRun {
			if err := os.Remove(filepath.Join(c.Source, filepath.FromSlash(c.Path))); err != nil {
				report.Failed = append(report.Failed, c.Path)
				return
			}
		}
		gone[key(c)] = true
		total -= c.Size
		perChannel[c.Channel] -= c.Size
		report.FreedBytes += c.Size
		report.Deleted = append(report.Deleted, Deleted{
			Source: c.Source, Path: c.Path, Channel: c.Channel, Size: c.Size, Rule: rule,
		})
	}

	// Per-channel lines first, oldest first within each channel.
	for _, c := range list {
		quota := cfg.ChannelQuotaBytes[c.Channel]
		if quota <= 0 || perChannel[c.Channel] <= quota {
			continue
		}
		remove(c, "channel")
	}

	// Then the global line, over whatever is left.
	if cfg.QuotaBytes > 0 {
		for _, c := range list {
			if total <= cfg.QuotaBytes {
				break
			}
			if gone[key(c)] {
				continue
			}
			remove(c, "global")
		}
	}
	return report, nil
}

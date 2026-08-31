package clips

// The scan cache: the walk's last answer, kept so a browser opening the app
// is answered from memory instead of waiting on a walk of every source. The
// directory stays the source of truth — the snapshot only decides how soon a
// listing reflects it:
//
//   - fresh enough (younger than TTL) → served as-is; the burst of requests
//     one app load fires shares a single walk between them
//   - older → served anyway, marked stale, and a rescan starts in the
//     background; the client is expected to ask again until the flag clears
//   - unusable (first run ever, or the source set changed) → the caller
//     waits on a real walk, one walk however many callers
//
// The snapshot also persists to disk (gzipped JSON in the data directory) so
// a freshly restarted server answers its first request from the previous
// run's walk. Like the play cache it is a convenience, never the record:
// deleting it loses nothing but the head start, and quota enforcement never
// reads it — footage is only ever deleted off a fresh walk.

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// DefaultTTL is how long a snapshot is served as current before a listing
// marks it stale and triggers a rescan. Long enough that one app load's
// burst of requests shares a walk; short enough that footage of "what just
// happened at the door" is never meaningfully out of date.
const DefaultTTL = 15 * time.Second

// snapshotVersion stamps the persisted file. A snapshot written by a build
// whose Clip shape differs decodes into wrong-looking listings, so an
// unknown version is discarded rather than trusted — the cost is one cold
// walk after an upgrade.
const snapshotVersion = 1

type snapshot struct {
	Version   int       `json:"version"`
	Sources   []string  `json:"sources"`
	ScannedAt time.Time `json:"scanned_at"`
	Missing   []string  `json:"missing,omitempty"`
	Clips     []Clip    `json:"clips"`
}

// Cache is a serve-stale-while-revalidate cache over ScanAll. The zero value
// works (in-memory only, DefaultTTL); Path adds persistence across restarts.
type Cache struct {
	// Path is where the snapshot persists between runs, or "" to keep it in
	// memory only. Reads and writes are best-effort: a missing or unwritable
	// file costs the head start, never an error.
	Path string
	// TTL is how long a snapshot is served without triggering a rescan;
	// 0 means DefaultTTL. (Negative means every hit is stale — only the
	// tests want that.)
	TTL time.Duration

	mu         sync.Mutex // guards snap, loaded, refreshing, gen
	snap       *snapshot
	loaded     bool // the disk snapshot has been looked for
	refreshing bool // a background rescan is already underway
	// gen is bumped by Invalidate. A walk that began before the bump saw
	// footage enforcement has since deleted, so its snapshot is discarded
	// rather than stored — the ghosts must not outlive the cleanup.
	gen int

	scanMu sync.Mutex // serialises walks — one walk, however many callers
}

// Listing is every clip under the sources, by way of the snapshot per the
// rules above. stale means a rescan is underway and the answer may not
// reflect the directory right now — ask again shortly. The returned slice is
// the caller's to mutate.
func (c *Cache) Listing(sources []string) (list []Clip, missing []string, stale bool) {
	ttl := c.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}

	c.mu.Lock()
	if !c.loaded {
		c.loaded = true
		c.snap = loadSnapshot(c.Path)
	}
	if s := c.snap; s != nil && equalStrings(s.Sources, sources) {
		if time.Since(s.ScannedAt) < ttl {
			defer c.mu.Unlock()
			return copyClips(s.Clips), copyStrings(s.Missing), false
		}
		if !c.refreshing {
			c.refreshing = true
			go c.refresh(copyStrings(sources), c.gen)
		}
		defer c.mu.Unlock()
		return copyClips(s.Clips), copyStrings(s.Missing), true
	}
	c.mu.Unlock()

	// Nothing servable — first run, or the source set changed. Walk now, but
	// one walk for everyone: whoever holds scanMu scans, the rest wait here
	// and find that walk's snapshot when they re-check.
	c.scanMu.Lock()
	defer c.scanMu.Unlock()
	c.mu.Lock()
	if s := c.snap; s != nil && equalStrings(s.Sources, sources) && time.Since(s.ScannedAt) < ttl {
		defer c.mu.Unlock()
		return copyClips(s.Clips), copyStrings(s.Missing), false
	}
	gen := c.gen
	c.mu.Unlock()

	snap := scanSnapshot(sources)
	c.mu.Lock()
	kept := c.gen == gen
	if kept {
		c.snap = snap
	}
	c.mu.Unlock()
	if kept {
		c.persist(snap)
	}
	return copyClips(snap.Clips), copyStrings(snap.Missing), false
}

// Invalidate drops the snapshot, memory and disk both — called after
// enforcement deletes footage, so no listing (this run's or a restarted
// server's) shows clips that are gone. The next Listing pays for a fresh
// walk, which is the point.
func (c *Cache) Invalidate() {
	c.mu.Lock()
	c.snap = nil
	c.loaded = true // and do not resurrect the disk copy
	c.gen++         // and discard any walk already underway — it saw the ghosts
	c.mu.Unlock()
	if c.Path != "" {
		os.Remove(c.Path)
	}
}

// refresh is the background rescan behind a stale answer.
func (c *Cache) refresh(sources []string, gen int) {
	c.scanMu.Lock()
	snap := scanSnapshot(sources)
	c.scanMu.Unlock()

	c.mu.Lock()
	kept := c.gen == gen
	if kept {
		c.snap = snap
	}
	c.refreshing = false
	c.mu.Unlock()
	if kept {
		c.persist(snap)
	}
}

func scanSnapshot(sources []string) *snapshot {
	list, missing := ScanAll(sources)
	return &snapshot{
		Version:   snapshotVersion,
		Sources:   copyStrings(sources),
		ScannedAt: time.Now(),
		Missing:   missing,
		Clips:     list,
	}
}

// persist writes the snapshot the way the config is written — temp file,
// then rename — but best-effort: a data directory that cannot take the cache
// costs the next restart its head start, nothing more. Gzip because the JSON
// is long repeated keys over thousands of clips; compressed it stays a few
// percent of the footage listing it describes.
func (c *Cache) persist(s *snapshot) {
	if c.Path == "" {
		return
	}
	tmp := c.Path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	zw := gzip.NewWriter(f)
	err = json.NewEncoder(zw).Encode(s)
	if e := zw.Close(); err == nil {
		err = e
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		os.Remove(tmp)
		return
	}
	os.Rename(tmp, c.Path)
}

// loadSnapshot reads a persisted snapshot, or nil for anything short of a
// well-formed current-version file — a cache is never worth an error.
func loadSnapshot(path string) *snapshot {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil
	}
	defer zr.Close()
	var s snapshot
	if json.NewDecoder(zr).Decode(&s) != nil || s.Version != snapshotVersion {
		return nil
	}
	return &s
}

// copyClips hands out a private slice: handlers filter listings in place and
// tag Remuxable onto rows, and none of that may write through into the
// snapshot other requests are being served from.
func copyClips(list []Clip) []Clip {
	out := make([]Clip, len(list))
	copy(out, list)
	return out
}

func copyStrings(list []string) []string {
	if list == nil {
		return nil
	}
	return append([]string(nil), list...)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

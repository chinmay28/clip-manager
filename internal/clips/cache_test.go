package clips

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCachedClip(t *testing.T, root, rel string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCacheServesSnapshotWithinTTL(t *testing.T) {
	root := t.TempDir()
	writeCachedClip(t, root, "cam/a.dav")
	c := &Cache{TTL: time.Hour}

	list, _, stale := c.Listing([]string{root})
	if len(list) != 1 || stale {
		t.Fatalf("first listing should walk fresh: %d clips, stale=%v", len(list), stale)
	}

	// A file landing after the walk is invisible until the TTL passes —
	// that is the trade the cache makes, and this pins it down.
	writeCachedClip(t, root, "cam/b.dav")
	list, _, stale = c.Listing([]string{root})
	if len(list) != 1 || stale {
		t.Fatalf("within the TTL the snapshot answers: %d clips, stale=%v", len(list), stale)
	}
}

func TestCacheServesStaleAndRefreshesInBackground(t *testing.T) {
	root := t.TempDir()
	writeCachedClip(t, root, "cam/a.dav")
	c := &Cache{TTL: -1} // every hit is stale: the background path, every time

	if list, _, _ := c.Listing([]string{root}); len(list) != 1 {
		t.Fatalf("first listing: want 1 clip, got %d", len(list))
	}
	writeCachedClip(t, root, "cam/b.dav")

	// The next answer is the old snapshot, honestly marked; the rescan it
	// kicked off delivers the new file to a later call.
	list, _, stale := c.Listing([]string{root})
	if len(list) != 1 || !stale {
		t.Fatalf("want the stale snapshot marked stale, got %d clips, stale=%v", len(list), stale)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		list, _, _ = c.Listing([]string{root})
		if len(list) == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("background rescan never landed: still %d clips", len(list))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCachePersistsAcrossRestarts(t *testing.T) {
	root := t.TempDir()
	writeCachedClip(t, root, "cam/a.dav")
	path := filepath.Join(t.TempDir(), "scancache.json.gz")

	c := &Cache{Path: path, TTL: time.Hour}
	if list, _, _ := c.Listing([]string{root}); len(list) != 1 {
		t.Fatal("seed listing failed")
	}

	// A "restarted server": a new Cache over the same file. Deleting the
	// clip behind its back proves the answer came from the snapshot, not a
	// walk.
	if err := os.Remove(filepath.Join(root, "cam/a.dav")); err != nil {
		t.Fatal(err)
	}
	c2 := &Cache{Path: path, TTL: time.Hour}
	list, _, stale := c2.Listing([]string{root})
	if len(list) != 1 || stale {
		t.Fatalf("want the persisted snapshot served, got %d clips, stale=%v", len(list), stale)
	}
}

func TestCacheRescansWhenSourcesChange(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeCachedClip(t, a, "cam/a.dav")
	writeCachedClip(t, b, "cam/b.dav")
	c := &Cache{TTL: time.Hour}

	if list, _, _ := c.Listing([]string{a}); len(list) != 1 {
		t.Fatal("seed listing failed")
	}
	// A snapshot of one source set must never answer for another — adding a
	// source walks fresh, however young the old snapshot is.
	list, _, stale := c.Listing([]string{a, b})
	if len(list) != 2 || stale {
		t.Fatalf("changed sources should walk fresh: %d clips, stale=%v", len(list), stale)
	}
}

func TestCacheInvalidateForgetsMemoryAndDisk(t *testing.T) {
	root := t.TempDir()
	writeCachedClip(t, root, "cam/a.dav")
	path := filepath.Join(t.TempDir(), "scancache.json.gz")
	c := &Cache{Path: path, TTL: time.Hour}

	if list, _, _ := c.Listing([]string{root}); len(list) != 1 {
		t.Fatal("seed listing failed")
	}
	if err := os.Remove(filepath.Join(root, "cam/a.dav")); err != nil {
		t.Fatal(err)
	}
	c.Invalidate()

	// Post-enforcement listings must reflect the deletion at once, this run
	// and the next alike.
	if list, _, stale := c.Listing([]string{root}); len(list) != 0 || stale {
		t.Fatalf("invalidate should force a fresh walk: %d clips, stale=%v", len(list), stale)
	}
	if _, err := os.Stat(path); err == nil {
		// The walk above persisted a fresh snapshot — that is fine; what
		// must not survive is the pre-invalidate one. Prove it by reading.
		if s := loadSnapshot(path); s != nil && len(s.Clips) != 0 {
			t.Fatal("the invalidated snapshot survived on disk")
		}
	}
}

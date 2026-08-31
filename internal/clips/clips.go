// Package clips reads the recordings directory: which clips exist, which
// camera each belongs to, and which of them a browser can play as-is.
//
// The directory is the source of truth and nothing else is kept — no index, no
// database. Security cameras (and the NVR software behind them) write a new
// file every few seconds and rotate old ones out; any record this package kept
// of that directory would be stale before it was written. Every listing is a
// fresh walk.
//
// Layout convention: the first path element under the clips root names the
// camera — `front-door/2026-08-30/12.00.01.dav` belongs to `front-door`. A
// file sitting directly in the root belongs to no camera and is grouped under
// an empty name. That is exactly how Dahua/Amcrest NVRs and most ffmpeg-based
// recorders lay their output out, and it is what per-camera quotas key on.
package clips

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Extensions this package recognises as camera footage. Everything else in the
// directory is invisible to the app — and, more importantly, untouchable by
// quota enforcement, which deletes only what this map admits.
//
// .dav is the Dahua/Amcrest container the cameras this app was built around
// write natively; the rest are what other NVRs and ffmpeg-based recorders
// produce.
var Extensions = map[string]bool{
	".dav":  true,
	".mp4":  true,
	".m4v":  true,
	".mov":  true,
	".mkv":  true,
	".avi":  true,
	".webm": true,
	".ts":   true,
	".flv":  true,
	".h264": true,
	".h265": true,
}

// playable is the subset a browser's <video> element can be handed directly.
// The others (.dav above all) go through the remux path instead — see
// RemuxCandidate.
var playable = map[string]bool{
	".mp4":  true,
	".m4v":  true,
	".mov":  true,
	".webm": true,
}

// RemuxCandidate reports whether a clip of this extension is worth handing to
// ffmpeg for on-the-fly repackaging into fragmented MP4: every recognised
// format a browser will not take directly. It is a candidacy, not a promise —
// repackaging changes the container and never the codec, so a stream the
// browser cannot decode (MJPEG in an old .avi, H.265 where the browser lacks
// it) still fails, in the player, with the download as the fallback. Whether
// candidacy becomes a Play button also depends on ffmpeg being on the
// machine, which is the server's knowledge, not this package's.
func RemuxCandidate(ext string) bool {
	return Extensions[ext] && !playable[ext]
}

// A Clip is one recording on disk.
type Clip struct {
	// Source is the source directory the clip lives under — one of the
	// configured roots, verbatim. Together with Path it is the clip's
	// identity: the API takes the pair back to serve or delete the file.
	Source string `json:"source"`
	// Path is relative to the clip's source, forward-slashed.
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Camera   string    `json:"camera"`
	Ext      string    `json:"ext"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	Playable bool      `json:"playable"`
	// Remuxable is filled in by the server, not the scan: it means "this
	// machine's ffmpeg can repackage this for the browser", and only the
	// server knows whether there is an ffmpeg.
	Remuxable bool `json:"remuxable"`
}

// ScanAll walks every source and returns the union of their clips, oldest
// first, each tagged with the source it came from. Oldest-first is not
// cosmetic: it is the order quota enforcement consumes the list in, and the
// invariant it relies on — across sources, so the oldest footage goes first
// wherever it sits.
//
// A source that cannot be read at all — an unplugged drive, a NAS that is
// down — contributes nothing rather than failing the union: the clips on the
// sources that ARE answering must stay visible, and quota enforcement on them
// must keep working. Which sources were unreadable is the second return; the
// server reports it rather than guessing.
func ScanAll(sources []string) ([]Clip, []string) {
	var out []Clip
	var missing []string
	for _, root := range sources {
		list, err := Scan(root)
		if err != nil {
			missing = append(missing, root)
			continue
		}
		for i := range list {
			list[i].Source = root
		}
		out = append(out, list...)
	}
	sortClips(out)
	return out, missing
}

// Scan walks one clips root and returns every clip under it, oldest first,
// with Source left empty — ScanAll is what tags it.
//
// Hidden files and directories (dot-prefixed) are skipped — sync tools and
// NVRs leave state files around, and none of it is footage. An unreadable
// subdirectory is skipped rather than failing the walk: one bad mount must
// not blank the whole listing.
func Scan(root string) ([]Clip, error) {
	var out []Clip
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil // skip what cannot be read, keep walking
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if !Extensions[ext] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		camera := ""
		if i := strings.IndexByte(rel, '/'); i >= 0 {
			camera = rel[:i]
		}
		out = append(out, Clip{
			Path:     rel,
			Name:     d.Name(),
			Camera:   camera,
			Ext:      ext,
			Size:     info.Size(),
			ModTime:  info.ModTime(),
			Playable: playable[ext],
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortClips(out)
	return out, nil
}

func sortClips(list []Clip) {
	sort.Slice(list, func(i, j int) bool {
		if !list[i].ModTime.Equal(list[j].ModTime) {
			return list[i].ModTime.Before(list[j].ModTime)
		}
		if list[i].Source != list[j].Source {
			return list[i].Source < list[j].Source
		}
		return list[i].Path < list[j].Path // a stable order when timestamps tie
	})
}

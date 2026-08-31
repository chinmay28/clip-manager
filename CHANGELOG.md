# Changelog

Releases are `vYEAR.MONTH.PATCH` — a calendar version, where the patch number
is the repository's commit count, so `v2026.8.42` is the 42nd commit on the
2026.8 line. See [`internal/version/version.go`](./internal/version/version.go).

Each section below is the body of the corresponding GitHub release. A heading
must name the tag exactly — a tag whose commit builds a different version is a
tag that shouldn't be published.

## Unreleased — the 2026.8 line

### Channels, days, and a clean front page

The home page is now the clips and nothing else — the *Sources* and *Storage*
panels moved to a **Settings** tab. The flat file list is gone: clips are
grouped into **channels**, read from the recordings' own names
(`N843A8_ch3_main_…` → channel `N843A8_ch3`, with the camera directory as the
fallback), and a channel can be **labeled** — select its chip, rename, and
"N843A8 ch3" becomes "Front door" in every browser, stored in the server's
config. Within a channel (or across all of them) clips are grouped by **day**
— *Today*, *Yesterday*, then dates — newest first, each row led by the time
the recording **started**, parsed from the filename's timestamp rather than
the upload's mtime.

### .dav plays in the browser

Formats a browser will not take directly — `.dav` above all — now play in
place: the server repackages the recording through the machine's own ffmpeg
into a cached, seekable `+faststart` MP4 served with range support — which is
what iOS Safari requires before it will play a remuxed stream at all (the
first cut of this feature streamed fragmented MP4, which desktop Chrome
accepted and iPhones refused). H.264 is container-copied; HEVC is copied and
tagged `hvc1`, the tag Safari insists on; codecs no browser decodes (MJPEG in
an old `.avi`) are transcoded to H.264, and the player falls back to a
server-side H.264 transcode automatically before it ever gives up on a clip.
Camera audio (usually G.711) is re-encoded to AAC so it survives the MP4.
Prepared MP4s are cached in the data directory (capped at 512 MB, oldest
evicted) so a clip is repackaged once, not once per view. The quickstart
installs ffmpeg by default (`INSTALL_FFMPEG=never` skips it); without one
those formats stay labelled downloads and the server says so at startup.

### One or more source directories

Clips can now come from **several source directories** at once — the NVR's
tree on the internal disk and the overflow on a USB drive, say. Sources named
on the command line (`--clips`, repeatable, or colon-separated
`CLIP_CLIPS_DIR`) are pinned; more can be added and removed in the app's new
*Sources* panel, which adopts existing directories and never creates or
deletes anything — removing a source only takes it out of view. Quotas span
the union: the total is measured across every source, enforcement retires the
oldest footage first wherever it sits, and a camera's name is its identity,
so the same camera split across two sources answers to one line. A source
that stops being readable (an unplugged drive) stays listed, is reported
rather than forgotten, drops out of the figures out loud, and is never
enforced against.

### The first cut

Clip Manager exists: a single static Go binary with an embedded web client
(installable to a phone's home screen as a PWA) over a directory of
security-camera clips.

- **Browse and play.** The clips directory is listed fresh on every load —
  one subdirectory per camera, the way NVRs write it. Browser-playable
  formats (`.mp4`, `.webm`, `.mov`, `.m4v`) play in place with scrubbing;
  `.dav` and the other camera containers are offered as downloads, honestly
  labelled, until the app grows remuxing.
- **Quotas.** A directory-wide quota, and per-camera quotas over it.
  Enforcement retires the oldest footage first — camera quotas before the
  global one, so a noisy camera pays for itself before a quiet one loses
  anything — and only ever touches files it recognises as footage. Every run
  reports exactly what went and why; a dry run answers "what would go"
  without touching anything. Runs hourly under `serve`, on demand from the
  app, and from the shell as `clip prune`.
- **One-command install.** `scripts/quickstart.sh` sets it up as a hardened
  systemd service on Ubuntu / Debian / Raspberry Pi OS, upgrades in place,
  snapshots the quota config before every upgrade, and rolls back a build
  that fails its health check. It never writes into the clips directory.

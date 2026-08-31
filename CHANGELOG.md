# Changelog

Releases are `vYEAR.MONTH.PATCH` — a calendar version, where the patch number
is the repository's commit count, so `v2026.8.42` is the 42nd commit on the
2026.8 line. See [`internal/version/version.go`](./internal/version/version.go).

Each section below is the body of the corresponding GitHub release. A heading
must name the tag exactly — a tag whose commit builds a different version is a
tag that shouldn't be published.

## Unreleased — the 2026.8 line

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

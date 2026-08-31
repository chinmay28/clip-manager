# Clip Manager

**A viewer and a budget for the directory your security cameras record into.**

Security cameras write a new clip every few seconds and never stop. Clip
Manager sits on that directory and does the two things it needs: shows you the
footage — `.dav` from Dahua/Amcrest hardware included — and keeps the
directory from eating the disk, with quotas that retire the oldest footage
first when a line is crossed.

Ships as a **single static Go binary** with an embedded web UI that installs
to a phone's home screen like an app.

```
┌ CLIP Manager ─────────────────────────────────────────────────────────┐
│ STORAGE   118.4 GB in 41,203 clips · quota 150 GB                     │
│ ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓░░░░░                    [Preview cleanup] [Now]  │
│   front-door   62.1 GB · 21,014        quota [ 80] GB                 │
│   driveway     41.9 GB · 15,466        quota [ 50] GB                 │
│   backyard     14.4 GB ·  4,723        quota [   ] GB                 │
├───────────────────────────────────────────────────────────────────────┤
│ [All] [front-door] [driveway] [backyard]                              │
│  12.31.42.dav   front-door · 3.1 MB · Aug 30, 12:31    dav  ↓        │
│  12.31.29.mp4   driveway · 2.4 MB · Aug 30, 12:31      ▶ Play        │
│  12.31.14.dav   front-door · 2.9 MB · Aug 30, 12:31    dav  ↓        │
└───────────────────────────────────────────────────────────────────────┘
```

---

## Quick start on Linux (Ubuntu / Raspberry Pi)

Install Clip Manager as a hardened **systemd service** with one command:

```bash
curl -fsSL https://raw.githubusercontent.com/chinmay28/clip-manager/main/scripts/quickstart.sh | sudo bash
```

(or, from a checkout: `sudo ./scripts/quickstart.sh`)

It installs Node 22 and Go if needed (both build-time only), creates a
dedicated `clip` system user, compiles the web client and the static server
binary, and runs it under systemd on `http://<host>:8124`, reachable from your
network.

Point it at footage your cameras already write:

```bash
curl -fsSL .../quickstart.sh | sudo CLIP_CLIPS_DIR=/mnt/nvr/footage bash
```

**Or skip the build entirely** and install the prebuilt binary from the latest
[release](https://github.com/chinmay28/clip-manager/releases) — no Node, no
Go, no source tree, seconds instead of minutes on a Raspberry Pi:

```bash
curl -fsSL https://raw.githubusercontent.com/chinmay28/clip-manager/main/scripts/quickstart.sh \
  | sudo CLIP_INSTALL=release bash
```

The download's checksum is verified before anything is swapped in, and
`CLIP_RELEASE=v2026.8.42` pins a specific release instead of the latest.
Releases publish **`linux/amd64`** and **`linux/arm64`**; anything else builds
from source (the default), which works everywhere. Both modes install the same
thing — one static binary with the web client embedded, under the same unit
and the same directories — so you can switch between them by re-running with a
different `CLIP_INSTALL`.

**Re-run it any time to upgrade — installs and upgrades are non-disruptive
and never touch your footage:**

- The clips directory and the quota config live at stable paths **outside**
  the source tree, so rebuilding or pulling can't clobber them. The installer
  never writes into the clips directory at all — deleting footage is quota
  enforcement's job, by design, and never a deploy's.
- Each upgrade quiesces the service and **snapshots the quota config** before
  swapping code in. The new build compiles while the old version keeps
  serving, so a failed build leaves the running app untouched.
- After restart it polls `/api/health`; if the new version is unhealthy it
  **rolls back** — to the previous commit when it built from source, to the
  previous binary when it installed a release — and restores the pre-upgrade
  config snapshot.

Override defaults with env vars (`PORT`, `HOST`, `CLIP_INSTALL`, `CLIP_REF`,
`CLIP_RELEASE`, `CLIP_DATA_DIR`, `CLIP_CLIPS_DIR`, `CLIP_MOUNT_ROOTS`,
`CLIP_PREFIX`, `CLIP_USER`, …). Manage it with `systemctl status clip` and
`journalctl -u clip -f`.

`PORT`, `HOST` and `CLIP_CLIPS_DIR` are **remembered**: on an upgrade, leaving
one unset keeps whatever the service is already running with rather than
resetting it, so re-running the script to pick up a new version can't quietly
move a loopback-only install back onto every interface — or point the service
away from your footage.

> **`HOST` defaults to `0.0.0.0`** — the service is reachable from your
> network as soon as it is installed, and there is **no authentication yet**:
> anyone who can reach the port can watch and download footage, redraw
> quotas, and trigger a cleanup that deletes recordings. Fine behind a home
> NAT you trust; anywhere else, set `HOST=127.0.0.1` and put TLS with auth in
> front (a reverse proxy, or Tailscale Serve).

---

## Quick start from source

```bash
# 1. Build (requires Go 1.25+ and Node.js 18+)
make build

# 2. Run it over the directory your cameras record into
./clip serve --clips /mnt/nvr/footage --port 8124
# → http://127.0.0.1:8124

# 3. Draw quotas in the app, or try a cleanup from the shell first
./clip prune --clips /mnt/nvr/footage --dry-run
```

The clips directory is laid out the way NVRs and ffmpeg-based recorders
already write it — **one subdirectory per camera**, clips inside:

```
footage/
├── front-door/2026-08-30/12.31.42.dav
├── driveway/2026-08-30/12.31.29.mp4
└── backyard/…
```

A file directly in the root belongs to no camera; it is listed, and covered by
the directory-wide quota, but a per-camera quota has nothing to meter it by.

---

## How the quota works

Two kinds of line can be drawn, both in the app (or by editing
`config.json` in the data directory):

- a **directory quota** — how much the whole clips directory may hold
- a **per-camera quota** — how much one camera's subdirectory may hold

When a line is crossed, enforcement deletes the **oldest footage first** until
the directory is back under it. Camera quotas run before the global one, so a
camera over its own line pays for itself before well-behaved cameras lose
anything to the shared total.

What keeps this safe to run unattended:

- **Only footage is ever eligible.** Enforcement deletes only files whose
  extension it recognises as camera output (`.dav`, `.mp4`, `.mkv`, `.avi`,
  `.ts`, …). A config file, a thumbnail cache, your notes — anything else in
  the directory is invisible to the quota and untouchable, however far over
  the line the directory is.
- **No quota, no deletion.** A fresh install enforces nothing until somebody
  draws a line.
- **Every run reports itself.** The app and the log both name every file that
  went and which line claimed it — an unattended job that removes footage in
  silence is not one anybody should trust.
- **Dry run first.** *Preview cleanup* in the app, `clip prune --dry-run` from
  the shell, or `POST /api/storage/enforce?dry_run=1` — all answer "what would
  go" without touching anything.

Enforcement runs hourly under `serve` (`--enforce-every` changes it, `0`
disables it), on demand from the app, and as a one-shot from the shell —
`clip prune` — for cron jobs and for trying things out.

---

## Formats, and the truth about `.dav`

Everything a camera plausibly writes is listed and served: `.dav`, `.mp4`,
`.m4v`, `.mov`, `.mkv`, `.avi`, `.webm`, `.ts`, `.flv`, `.h264`, `.h265`.

What a **browser** will actually decode is a shorter list — `.mp4`, `.webm`,
`.mov`, `.m4v` — and those play in place, with scrubbing (the server honours
range requests). The rest, `.dav` above all, are offered as **downloads**,
labelled by format, rather than a play button that spins forever: `.dav` is
Dahua's proprietary container and no browser decodes it. Play the downloaded
file in VLC, or remux it once with ffmpeg
(`ffmpeg -i clip.dav -c copy clip.mp4`). Teaching the server to do that
remuxing itself is the obvious next feature; until it exists the app doesn't
pretend otherwise.

---

## API

Everything the app does goes through six JSON endpoints, which are just as
usable from a script:

| Endpoint | What |
|---|---|
| `GET /api/health` | `{status, version, clips_dir}` — what the installer polls |
| `GET /api/clips` | every clip: path, camera, size, mtime, playable |
| `GET /api/clip?path=…` | one clip's bytes, with range support |
| `GET /api/storage` | usage (total and per camera) + the current quota config |
| `PUT /api/storage/config` | replace the quota config |
| `POST /api/storage/enforce` | run enforcement now; `?dry_run=1` to only report |

---

## Building from Source

| Tool | Minimum | Install |
|---|---|---|
| Go | 1.25 | https://go.dev/dl/ |
| Node.js | 18 | https://nodejs.org |

```bash
make build        # frontend + binary
make build-web    # frontend only → internal/server/dist/
make build-go     # binary only
make version      # print the version this tree would build as
make test         # Go unit tests
make release      # cross-compile all platforms → dist/
```

Output is `clip` on Linux/macOS, `clip.exe` on Windows.

### Versioning

`vYEAR.MONTH.PATCH` — a calendar version, where **the patch number is the
repository's commit count** — so `v2026.8.42` is the 42nd commit on the 2026.8
line. There is no semantic major/minor: the leading numbers say *when* a
release line opened, not what it promises about compatibility. Breaking
changes are called out in [`CHANGELOG.md`](./CHANGELOG.md), which is the thing
to read before upgrading.

- `YEAR`/`MONTH` are source constants in
  [`internal/version/version.go`](./internal/version/version.go). Bump them by
  hand when a release line opens — they are deliberately not read from the
  build clock, so rebuilding an old tree still reports what it originally
  shipped.
- The month is not zero-padded (`v2026.8.42`, not `v2026.08.42`): semver
  forbids a leading zero, and an unpadded month keeps every tag something a
  semver parser will accept.
- `PATCH` only exists at build time, so it is stamped in: `-ldflags -X` for
  the Go binary, Vite's `define` for the web bundle. Both read
  [`scripts/version.mjs`](./scripts/version.mjs), so the header,
  `clip version` and `/api/health` can never disagree.

A patch of `0` means an unstamped build — no git, or a **shallow clone**,
which `version.mjs` detects and refuses to guess around rather than shipping a
build that quietly calls itself `v2026.8.1`. Anything building a release needs
the full commit graph (`fetch-depth: 0`, or `--filter=blob:none` rather than
`--depth 1`).

---

## Project Structure

```
clip-manager/
├── cmd/clip/                    # CLI: serve, prune, version
├── internal/
│   ├── clips/                   # the directory walk: clips, cameras, playability
│   ├── storage/                 # quotas: config, usage, oldest-first enforcement
│   ├── server/                  # JSON API + embedded SPA (dist/ is committed)
│   └── version/                 # YEAR/MONTH; PATCH stamped at link time
├── web/
│   ├── src/                     # React client: App, theme, api
│   │   └── components/Brand.jsx # the wordmark + the developer badge
│   ├── public/                  # app icon, home-screen icons + manifest,
│   │                            #   developer badge
│   ├── fonts/                   # the wordmark's script face (subset + OFL)
│   └── build-version.js         # feeds the version into the bundle
├── scripts/
│   ├── quickstart.sh            # one-command systemd install / upgrade / rollback
│   ├── version.mjs              # the one place the version is assembled
│   ├── make-icons.mjs           # redraws the home-screen PNGs from icon.svg
│   ├── make-wordmark-font.py    # cuts the wordmark's script face down
│   └── build-release.sh         # cross-compile all platforms
├── CHANGELOG.md                 # release notes, one section per tag
├── LICENSE                      # AGPL-3.0-only
└── Makefile
```

The home-screen icons are drawn once, in
[`icon.svg`](./web/public/icon.svg), and re-rendered to the committed PNGs by
[`scripts/make-icons.mjs`](./scripts/make-icons.mjs) — change the SVG, run
`make icons`, commit what falls out; `make icons-check` fails CI when they
drift.

---

## Development

```bash
# Terminal 1 — Go server
make build-go && ./clip serve --port 8124 --clips /tmp/dev-clips

# Terminal 2 — hot-reload frontend (proxies /api/* to :8124)
cd web && npm run dev     # → http://localhost:5173
```

---

## License

Clip Manager is free software licensed under the **GNU Affero General Public
License v3.0** (`AGPL-3.0-only`). See [LICENSE](./LICENSE) for the full text.

The AGPL is a strong copyleft license: anyone who distributes Clip Manager —
or **runs a modified version as a network service** — must make the complete
corresponding source available under the same license. Copyright in the
project is held by Chinmay Manjunath, who may also offer Clip Manager under
separate commercial terms.

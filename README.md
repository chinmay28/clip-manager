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
┌ Clip Manager ──────────────────────────────── [Clips] [Settings] ─────┐
│ [All] [Front door 709] [Driveway 421] [Backyard 388]      ✏️ Rename   │
│ [Today] [Yest.] [Aug 29] [Aug 28] [Aug 27] …  ←scrolls                │
│ [ 285 ] [ 292 ] [ 281 ]  [ 279 ]  [ 282 ]                             │
│                                                                       │
│ Today · 285 clips · 1.4 GB                                            │
│ ▾ 10 PM   16 clips · 83 MB                                            │
│    10:59:46 PM · Front door   6.4 MB · …_ch3_main_202…     ▶ Play    │
│    10:56:06 PM · Driveway     3.6 MB · …_ch1_main_202…     ▶ Play    │
│ ▸ 9 PM    22 clips · 108 MB                                           │
│ ▸ 8 PM    19 clips · 96 MB                                            │
└───────────────────────────────────────────────────────────────────────┘
```

The home page is the footage, organized for cameras that write **hundreds of
clips a day**: drill down by **channel** (read from the recordings' own
filenames, labelable to names that mean something), then by **day** on a
scrollable strip with per-day counts, then by collapsible **hour**. Each row
is led by when the recording started. Only the chosen day's clips are ever
fetched — the menus are drawn from a lightweight summary API. Sources and
quotas live behind the **Settings** tab.

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

Point it at footage your cameras already write — one directory or several,
colon-separated like `PATH`:

```bash
curl -fsSL .../quickstart.sh | sudo CLIP_CLIPS_DIR=/mnt/nvr/footage:/media/usb/overflow bash
```

Directories named this way are **pinned** — always in the source set, not
removable from a browser. More sources can be added and removed later in the
app itself (see [Sources](#sources)).

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

# 2. Run it over the directories your cameras record into — repeat --clips
#    for several sources
./clip serve --clips /mnt/nvr/footage --clips /media/usb/overflow --port 8124
# → http://127.0.0.1:8124

# 3. Draw quotas in the app, or try a cleanup from the shell first
./clip prune --clips /mnt/nvr/footage --clips /media/usb/overflow --dry-run
```

Each source directory is laid out the way NVRs and ffmpeg-based recorders
already write it — **one subdirectory per camera**, clips inside:

```
footage/
├── front-door/2026-08-30/12.31.42.dav
├── driveway/2026-08-30/12.31.29.mp4
└── backyard/…
```

A file directly in a source's root belongs to no camera; it is listed, and
covered by the total quota, but a per-camera quota has nothing to meter it by.

## Sources

The clips can come from **one or more source directories** — the NVR's tree
on the internal disk and the overflow on a USB drive, say. The set is the
union of two lists:

- **Pinned** sources, named on the command line (`--clips`, repeated) or in
  `CLIP_CLIPS_DIR` (colon-separated). The app shows them and cannot remove
  them — what the operator pinned at launch outranks a click in a browser.
- Sources **added in the app** (the *Sources* panel on the Settings tab, or
  `POST /api/sources`),
  stored in `config.json`. These can be removed again the same way. The
  directory must already exist: adding adopts footage, it never invents a
  place for it. Removing a source deletes nothing — it takes the directory
  out of view and out from under the quotas, and the files stay exactly where
  they are.

With nothing named anywhere, `<data>/clips` is created and used, so a fresh
install always has somewhere to point a camera at.

A source that stops being readable — an unplugged drive, a NAS that is down —
stays listed and is reported as unreadable rather than forgotten: its clips
drop out of the listing and **out of the quota figures** (the app says so, in
so many words, since a total that silently halves reads as deleted footage),
and enforcement never acts on footage it cannot see. Plug it back in and
everything reappears.

---

## How the quota works

Two kinds of line can be drawn, both in the app (or by editing
`config.json` in the data directory):

- a **total quota** — how much footage all the sources together may hold
- a **per-camera quota** — how much one camera may hold. The camera's *name*
  is its identity: the same directory name under two sources is one camera to
  its quota, deliberately, so a camera whose footage is split across a disk
  and its overflow still answers to one line.

When a line is crossed, enforcement deletes the **oldest footage first,
across every source** — the oldest goes first wherever it sits — until the
figure is back under it. Camera quotas run before the global one, so a camera
over its own line pays for itself before well-behaved cameras lose anything
to the shared total.

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

## Formats, and how `.dav` plays

Everything a camera plausibly writes is listed and served: `.dav`, `.mp4`,
`.m4v`, `.mov`, `.mkv`, `.avi`, `.webm`, `.ts`, `.flv`, `.h264`, `.h265`.

What a **browser** will take directly is a shorter list — `.mp4`, `.webm`,
`.mov`, `.m4v` — and those play in place, with scrubbing (the server honours
range requests).

The rest, **`.dav` above all**, play through the machine's own ffmpeg: the
server repackages the recording into a cached, seekable `+faststart` MP4 and
serves it with range support — the shape iOS Safari insists on before it will
play a remuxed stream at all. H.264 is **container-copied, not re-encoded** —
a Dahua camera's H.264 is already something a browser decodes, it is only the
container the browser refuses, and a copy is cheap enough for a Raspberry Pi.
HEVC is copied too, tagged `hvc1` (the tag Safari requires); a codec no
browser decodes — MJPEG in an old `.avi` — is transcoded to H.264, and the
player retries with a server-side H.264 transcode (`?transcode=1`) before it
gives up on any clip. Camera audio (usually G.711) is re-encoded to AAC so it
survives the MP4. Prepared MP4s are cached under the data directory
(`playcache/`, capped at 512 MB, least recently touched evicted), so a clip
is repackaged once, not once per view, and scrubbing works everywhere. The
row keeps its format badge so the difference stays explicable.

A recording that defeats all of that — a damaged file, ffmpeg missing —
still fails in the player as a sentence with the download beside it rather
than a black rectangle. VLC plays anything the camera wrote.

This is the same arrangement as SAND Vault shelling out to git: the app
carries no codecs of its own, it drives the ffmpeg on the machine — **a clip
ffmpeg can read here is one the app can play**. `scripts/quickstart.sh`
installs ffmpeg by default (`INSTALL_FFMPEG=never` skips it); without one,
those formats are offered as labelled downloads, the server says so at
startup, and everything else works.

---

## API

Everything the app does goes through six JSON endpoints, which are just as
usable from a script:

| Endpoint | What |
|---|---|
| `GET /api/health` | `{status, version, sources, ffmpeg}` — what the installer polls |
| `GET /api/clips` | clips across every source: source, path, camera, channel, start time, size, mtime, playable, remuxable — plus which sources were unreadable and the channel labels; `?day=YYYY-MM-DD` and `?channel=` filter, which is how the app fetches (one day at a time) |
| `GET /api/summary` | the archive without its weight: clip/byte counts per channel and per day (`?channel=` scopes the days), newest day first — what the app draws its menus from |
| `GET /api/clip?source=…&path=…` | one clip's bytes, with range support; the source must be a configured one |
| `GET /api/clip/play?source=…&path=…` | the clip repackaged through ffmpeg into a cached, seekable MP4 (`.dav` and friends); `&transcode=1` forces H.264; 422 with ffmpeg's own words when the file defeats it |
| `PUT /api/channels/label` | name a channel `{channel, label}` — an empty label forgets the name |
| `GET /api/sources` | the source set: path, pinned, available |
| `POST /api/sources` | add a source `{path}` — must exist and be absolute |
| `DELETE /api/sources?path=…` | forget a runtime-added source (files untouched; pinned ones refuse) |
| `GET /api/storage` | usage (total, per camera, per source) + the current quota config |
| `PUT /api/storage/config` | replace the quota config (sources are untouched — they have their own endpoints) |
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

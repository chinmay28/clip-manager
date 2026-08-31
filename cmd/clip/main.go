// Command clip is the Clip Manager CLI and server.
//
//	clip serve --clips /var/lib/clip/clips --port 8124
//	clip prune --clips /var/lib/clip/clips [--dry-run]
//	clip version
//
// One static binary: `serve` embeds the built web client, so deploying the
// app is copying this file somewhere and running it.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chinmay28/clip-manager/internal/server"
	"github.com/chinmay28/clip-manager/internal/storage"
	"github.com/chinmay28/clip-manager/internal/version"
)

func main() {
	log.SetFlags(log.LstdFlags)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "prune":
		cmdPrune(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version.String())
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "clip: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Clip Manager — view security-camera clips and keep their directory in budget.

Usage:
  clip serve  [flags]   run the web app and the quota schedule
  clip prune  [flags]   run quota enforcement once, from the shell
  clip version          print the version

Flags for serve:
  --clips DIR           a directory the cameras record into. Repeat the flag
                        for several sources (or set $CLIP_CLIPS_DIR to a
                        colon-separated list). More can be added and removed
                        in the app; the ones given here are pinned. With none
                        given anywhere, <data>/clips is used.
  --data DIR            where the quota config lives
                        (default $CLIP_DATA_DIR, else ~/.clip)
  --port N              listen port (default 8124)
  --bind ADDR           bind address (default 0.0.0.0)
  --enforce-every DUR   how often quotas are enforced (default 1h; 0 = never)

Flags for prune:
  --clips DIR, --data DIR   as above
  --dry-run                 report what would be deleted, delete nothing
`)
}

// dataDir resolves where the config lives: flag, then $CLIP_DATA_DIR, then
// ~/.clip — the systemd unit sets the variable, a shell run gets the home.
func dataDir(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("CLIP_DATA_DIR"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".clip"
	}
	return filepath.Join(home, ".clip")
}

// multiFlag collects a repeatable string flag: --clips /a --clips /b.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ":") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// pinnedSources resolves the source directories fixed for this run: the
// --clips flags when any were given, else $CLIP_CLIPS_DIR split on ':' (the
// PATH convention — one variable, several directories). Empty means the
// command line pinned nothing, which is a real answer: sources may still come
// from the config, and only when THAT is empty too does the caller fall back
// to <data>/clips.
func pinnedSources(flags multiFlag) []string {
	if len(flags) > 0 {
		return flags
	}
	var out []string
	for _, p := range strings.Split(os.Getenv("CLIP_CLIPS_DIR"), ":") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolveSources settles the pinned source set and the data directory for one
// run, creating the built-in default only when NOTHING else names a source —
// the fresh-install case, where the cameras have not written anything yet and
// a server that refused to start until they did would be impossible to set up
// through. Explicitly named directories are the opposite case: they are
// expected to hold footage already, and a missing one is warned about rather
// than invented, because an empty stand-in would report "no clips" instead of
// the typo.
func resolveSources(flags multiFlag, dataFlag string) (pinned []string, data string) {
	data = dataDir(dataFlag)
	if err := os.MkdirAll(data, 0o755); err != nil {
		log.Fatalf("data directory %s: %v", data, err)
	}

	pinned = pinnedSources(flags)
	if len(pinned) == 0 {
		cfg, err := storage.Load(filepath.Join(data, "config.json"))
		if err != nil {
			log.Fatal(err)
		}
		if len(cfg.Sources) > 0 {
			return nil, data // the config carries the sources; nothing pinned
		}
		fallback := filepath.Join(data, "clips")
		if err := os.MkdirAll(fallback, 0o755); err != nil {
			log.Fatalf("clips directory %s: %v", fallback, err)
		}
		return []string{fallback}, data
	}
	for _, src := range pinned {
		if info, err := os.Stat(src); err != nil || !info.IsDir() {
			log.Printf("warning: source %s is not a readable directory right now — serving without its clips until it appears", src)
		}
	}
	return pinned, data
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var clipsFlags multiFlag
	fs.Var(&clipsFlags, "clips", "a directory the cameras record into (repeatable)")
	dataFlag := fs.String("data", "", "directory the quota config lives in")
	port := fs.Int("port", 8124, "listen port")
	bind := fs.String("bind", "0.0.0.0", "bind address")
	every := fs.Duration("enforce-every", time.Hour, "how often quotas are enforced (0 disables)")
	fs.Parse(args)

	pinned, data := resolveSources(clipsFlags, *dataFlag)

	// The machine's own ffmpeg is what turns a .dav from a download into
	// something the browser plays — the same arrangement as shelling out to
	// git: a clip ffmpeg can read here is one the app can play. Its absence
	// is a state to announce at startup, not to discover click by click.
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		ffmpeg = ""
		log.Printf("ffmpeg not found — .dav and other camera formats will download instead of playing; install ffmpeg and restart to change that")
	}

	srv := &server.Server{
		PinnedSources: pinned,
		ConfigPath:    filepath.Join(data, "config.json"),
		FFmpeg:        ffmpeg,
	}
	if *every > 0 {
		go srv.EnforceLoop(*every)
	}

	addr := fmt.Sprintf("%s:%d", *bind, *port)
	described := strings.Join(pinned, ", ")
	if described == "" {
		described = "the sources in " + srv.ConfigPath
	}
	log.Printf("Clip Manager %s — serving %s on http://%s", version.String(), described, addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}

func cmdPrune(args []string) {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	var clipsFlags multiFlag
	fs.Var(&clipsFlags, "clips", "a directory the cameras record into (repeatable)")
	dataFlag := fs.String("data", "", "directory the quota config lives in")
	dryRun := fs.Bool("dry-run", false, "report what would be deleted, delete nothing")
	fs.Parse(args)

	pinned, data := resolveSources(clipsFlags, *dataFlag)
	cfg, err := storage.Load(filepath.Join(data, "config.json"))
	if err != nil {
		log.Fatal(err)
	}
	if cfg.QuotaBytes <= 0 && len(cfg.CameraQuotaBytes) == 0 {
		fmt.Println("no quota configured — nothing to enforce")
		return
	}
	// The shell sees the same source set the server would: pinned plus
	// whatever the app added to the config, deduplicated — the same
	// directory named twice would be scanned twice and its footage counted
	// double against the quota.
	seen := map[string]bool{}
	var sources []string
	for _, src := range append(append([]string{}, pinned...), cfg.Sources...) {
		src = filepath.Clean(src)
		if !seen[src] {
			seen[src] = true
			sources = append(sources, src)
		}
	}
	report, err := storage.Enforce(sources, cfg, *dryRun)
	if err != nil {
		log.Fatal(err)
	}
	verb := "deleted"
	if *dryRun {
		verb = "would delete"
	}
	for _, d := range report.Deleted {
		fmt.Printf("%s  %s (%s, %s quota)\n", verb, filepath.Join(d.Source, d.Path), formatBytes(d.Size), d.Rule)
	}
	for _, f := range report.Failed {
		fmt.Printf("could not remove %s\n", f)
	}
	fmt.Printf("%s %d clip(s), %s\n", verb, len(report.Deleted), formatBytes(report.FreedBytes))
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

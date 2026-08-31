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
	"path/filepath"
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
  --clips DIR           the directory the cameras record into
                        (default $CLIP_CLIPS_DIR, else <data>/clips)
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

func clipsDir(flagValue, data string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("CLIP_CLIPS_DIR"); env != "" {
		return env
	}
	return filepath.Join(data, "clips")
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	clipsFlag := fs.String("clips", "", "directory the cameras record into")
	dataFlag := fs.String("data", "", "directory the quota config lives in")
	port := fs.Int("port", 8124, "listen port")
	bind := fs.String("bind", "0.0.0.0", "bind address")
	every := fs.Duration("enforce-every", time.Hour, "how often quotas are enforced (0 disables)")
	fs.Parse(args)

	data := dataDir(*dataFlag)
	clips := clipsDir(*clipsFlag, data)
	// The clips directory is created rather than required: on a fresh
	// install the cameras have not written anything yet, and a server that
	// refuses to start until they do would be impossible to set up through.
	if err := os.MkdirAll(clips, 0o755); err != nil {
		log.Fatalf("clips directory %s: %v", clips, err)
	}
	if err := os.MkdirAll(data, 0o755); err != nil {
		log.Fatalf("data directory %s: %v", data, err)
	}

	srv := &server.Server{
		ClipsDir:   clips,
		ConfigPath: filepath.Join(data, "config.json"),
	}
	if *every > 0 {
		go srv.EnforceLoop(*every)
	}

	addr := fmt.Sprintf("%s:%d", *bind, *port)
	log.Printf("Clip Manager %s — serving %s on http://%s", version.String(), clips, addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}

func cmdPrune(args []string) {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	clipsFlag := fs.String("clips", "", "directory the cameras record into")
	dataFlag := fs.String("data", "", "directory the quota config lives in")
	dryRun := fs.Bool("dry-run", false, "report what would be deleted, delete nothing")
	fs.Parse(args)

	data := dataDir(*dataFlag)
	clips := clipsDir(*clipsFlag, data)
	cfg, err := storage.Load(filepath.Join(data, "config.json"))
	if err != nil {
		log.Fatal(err)
	}
	if cfg.QuotaBytes <= 0 && len(cfg.CameraQuotaBytes) == 0 {
		fmt.Println("no quota configured — nothing to enforce")
		return
	}
	report, err := storage.Enforce(clips, cfg, *dryRun)
	if err != nil {
		log.Fatal(err)
	}
	verb := "deleted"
	if *dryRun {
		verb = "would delete"
	}
	for _, d := range report.Deleted {
		fmt.Printf("%s  %s (%s, %s quota)\n", verb, d.Path, formatBytes(d.Size), d.Rule)
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

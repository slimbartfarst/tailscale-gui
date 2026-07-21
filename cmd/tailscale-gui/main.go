// cmd/tailscale-gui/main.go
//
// Entry point. Connects to tailscaled, then hands the main thread to the
// systray library (which requires it on Linux via AppIndicator/D-Bus).
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/yourname/tailscale-gui/internal/client"
	"github.com/yourname/tailscale-gui/internal/config"
	"github.com/yourname/tailscale-gui/internal/notify"
	"github.com/yourname/tailscale-gui/internal/routes"
	"github.com/yourname/tailscale-gui/internal/systray"
	"github.com/yourname/tailscale-gui/internal/taildrop"
	"github.com/yourname/tailscale-gui/internal/window"
)

func main() {
	socketPath := flag.String("socket", "", "tailscaled socket path (auto-detected if empty)")
	verbose := flag.Bool("v", false, "verbose logging")
	flag.Parse()

	if !*verbose {
		log.SetFlags(log.Ltime | log.Lshortfile)
	}

	// ── Config ───────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		log.Printf("warn: config load failed, using defaults: %v", err)
		cfg = config.Default()
	}
	if *socketPath != "" {
		cfg.SocketPath = *socketPath
	}

	// ── Context / signals ─────────────────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		log.Println("signal received, shutting down")
		cancel()
	}()

	// ── Tailscale local client ────────────────────────────────────────────────
	tsClient, err := client.New(ctx, cfg.SocketPath)
	if err != nil {
		log.Fatalf("cannot reach tailscaled: %v\n\n"+
			"Make sure tailscaled is running:\n  sudo tailscaled &\n\n"+
			"And that you have operator permission:\n  sudo tailscale set --operator=$USER\n", err)
	}

	// ── Taildrop receiver ─────────────────────────────────────────────────────
	tdrop := taildrop.New(tsClient, cfg.TaildropDir)

	// ── Notifications ─────────────────────────────────────────────────────────
	notifier := notify.New(cfg.NotificationsEnabled)

	// ── Status window (localhost HTTP server, opened on demand) ───────────────
	win := window.New(ctx, tsClient, cfg.StatusWindowPort)

	// ── Systray (blocks; owns main thread) ────────────────────────────────────
	app := systray.New(ctx, tsClient, cfg, notifier, tdrop, win)

	// Wire the browser "Send file" button back into the systray send flow.
	win.SendFileFn = app.SendFileToPeerID

	// Wire subnet route callbacks so the browser dashboard can read/write routes.
	rm := routes.New(tsClient)
	win.RoutesFn = func(c context.Context) ([]window.RouteEntry, error) {
		rs, err := rm.Current(c)
		if err != nil {
			return nil, err
		}
		out := make([]window.RouteEntry, len(rs))
		for i, r := range rs {
			out[i] = window.RouteEntry{
				Prefix:   r.Prefix.String(),
				Approved: r.Approved,
				Label:    r.Label,
			}
		}
		return out, nil
	}
	win.AddRouteFn = func(c context.Context, cidr string) error {
		pfx, err := routes.ParsePrefix(cidr)
		if err != nil {
			return err
		}
		if err := routes.Validate(pfx.String()); err != nil {
			return err
		}
		return rm.Add(c, pfx)
	}
	win.RemoveRouteFn = func(c context.Context, cidr string) error {
		pfx, err := routes.ParsePrefix(cidr)
		if err != nil {
			return err
		}
		return rm.Remove(c, pfx)
	}

	app.Run()

	log.Println("goodbye")
}

package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	dataDir := flag.String("data", "data", "directory for game snapshots")
	flag.Parse()

	hub, err := newHub(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := hub.restore(); err != nil {
		log.Printf("restore: %v", err)
	}

	// Serve the UI from ./web when present (live-editable without a
	// rebuild); fall back to the copy embedded in the binary.
	var fsys fs.FS
	if _, err := os.Stat("web/index.html"); err == nil {
		fsys = os.DirFS("web")
	} else {
		fsys, _ = fs.Sub(webFS, "web")
	}
	s := newServer(hub, fsys)

	go func() { // paced bot play across all rooms
		for range time.Tick(800 * time.Millisecond) {
			for _, r := range hub.snapshot() {
				r.G.BotStep()
			}
		}
	}()
	go func() { // janitor: expire idle rooms, sweep limiter maps, persist stats
		for range time.Tick(5 * time.Minute) {
			hub.expire()
			hub.stats.save()
			s.globalRL.sweep(time.Hour)
			s.createRL.sweep(time.Hour)
			s.apiRL.sweep(time.Hour)
			s.logRL.sweep(time.Hour)
		}
	}()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		// Bounds slow-body drip attacks. Applies to reading the request
		// only — SSE response streaming is unaffected.
		ReadTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("Mitayshvim server listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Println("shutting down: snapshotting rooms")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		srv.Close() // SSE streams never end on their own — cut them
	}
	hub.stopAll() // stop fanout goroutines so saveAll's snapshots are final
	hub.saveAll()
	hub.stats.save()
}

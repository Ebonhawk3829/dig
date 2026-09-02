// Command dig is the album-first music browser for slskd.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Ebonhawk3829/dig/internal/config"
	"github.com/Ebonhawk3829/dig/internal/library"
	"github.com/Ebonhawk3829/dig/internal/mb"
	"github.com/Ebonhawk3829/dig/internal/notify"
	"github.com/Ebonhawk3829/dig/internal/slskd"
	"github.com/Ebonhawk3829/dig/internal/store"
	"github.com/Ebonhawk3829/dig/internal/web"
)

func main() {
	configPath := flag.String("config", os.Getenv("DIG_CONFIG"), "path to dig.yaml")
	addr := flag.String("addr", "", "listen address (overrides config port)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// metadata cache (best-effort: dig works without it)
	var cache *store.Cache
	if cfg.DB.Path != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.DB.Path), 0o755); err == nil {
			if c, err := store.Open(cfg.DB.Path); err == nil {
				defer c.Close()
				cache = c
				// periodic purge of expired cache entries
				go func() {
					for range time.Tick(10 * time.Minute) {
						if n, err := c.PurgeExpired(); err == nil && n > 0 {
							log.Printf("cache: purged %d expired entries", n)
						}
					}
				}()
			} else {
				log.Printf("cache: %v (continuing without cache)", err)
			}
		}
	}

	// MusicBrainz client (nil-safe: web layer degrades gracefully)
	mbClient := mb.New(
		cfg.MusicBrainz.BaseURL,
		cfg.MusicBrainz.UserAgent,
		time.Duration(cfg.MusicBrainz.CacheTTLSearch)*time.Second,
		time.Duration(cfg.MusicBrainz.CacheTTLDetail)*time.Second,
		cache,
	)

	client := slskd.New(cfg.Slskd.URL, cfg.Slskd.APIKey)
	guard := slskd.NewInFlightGuard(client)

	// optional library check (Layer 2 dedup); nil when unconfigured
	libClient := library.New(cfg.Library.SubsonicURL, cfg.Library.SubsonicUser, cfg.Library.SubsonicPassword)

	// optional push notifications on download terminal states
	notifier := notify.New(cfg.Notify.URL)

	srv, err := web.New(cfg, client, guard, mbClient, cache, libClient, notifier)
	if err != nil {
		log.Fatalf("web: %v", err)
	}

	// background transfer watcher: fires notify on completed/failed/partial
	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	go srv.WatchTransfers(watchCtx)

	if *addr == "" {
		*addr = fmt.Sprintf(":%d", cfg.Server.Port)
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("dig listening on %s (slskd: %s)", *addr, cfg.Slskd.URL)
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// ensureDir is a small helper used by tests and future store wiring.
func ensureDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

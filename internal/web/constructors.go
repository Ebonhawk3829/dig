package web

import (
	"time"

	"github.com/Ebonhawk3829/dig/internal/config"
	"github.com/Ebonhawk3829/dig/internal/library"
	"github.com/Ebonhawk3829/dig/internal/mb"
	"github.com/Ebonhawk3829/dig/internal/notify"
	"github.com/Ebonhawk3829/dig/internal/slskd"
	"github.com/Ebonhawk3829/dig/internal/store"
)

// newSlskdClient builds a slskd client from config (test/constructor helper).
func newSlskdClient(cfg config.Config) *slskd.Client {
	return slskd.New(cfg.Slskd.URL, cfg.Slskd.APIKey)
}

// newGuard builds an in-flight guard around a client.
func newGuard(c *slskd.Client) *slskd.InFlightGuard {
	return slskd.NewInFlightGuard(c)
}

// newMBClient builds a MusicBrainz client from config.
func newMBClient(cfg config.Config, cache *store.Cache) *mb.Client {
	return mb.New(
		cfg.MusicBrainz.BaseURL,
		cfg.MusicBrainz.UserAgent,
		time.Duration(cfg.MusicBrainz.CacheTTLSearch)*time.Second,
		time.Duration(cfg.MusicBrainz.CacheTTLDetail)*time.Second,
		cache,
	)
}

// newLibraryClient builds a library client from config (nil if unconfigured).
func newLibraryClient(cfg config.Config) *library.Client {
	return library.New(cfg.Library.SubsonicURL, cfg.Library.SubsonicUser, cfg.Library.SubsonicPassword)
}

// newNotifySender builds a notify sender from config (nil if unconfigured).
func newNotifySender(cfg config.Config) *notify.Sender {
	return notify.New(cfg.Notify.URL)
}

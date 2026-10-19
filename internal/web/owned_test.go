package web

import (
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/dig/internal/slskd"
	"github.com/Ebonhawk3829/dig/internal/store"
)

// The slskd account is shared with other tools (e.g. rhapsode), so the read
// paths must only credit transfers dig enqueued — the same ownership marks the
// notification watcher uses.
func TestOwnedDownloadsFiltersForeignTransfers(t *testing.T) {
	cache, err := store.Open(filepath.Join(t.TempDir(), "dig.db"))
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	defer cache.Close()

	// dig enqueued its own album from "peer".
	if err := cache.MarkOwn("peer", `Music\Toto\Toto IV`); err != nil {
		t.Fatalf("mark own: %v", err)
	}

	s := &Server{cache: cache}
	downloads := map[string][]slskd.Transfer{
		"peer": {
			{Filename: `Music\Toto\Toto IV\01 - Rosanna.flac`},           // dig's
			{Filename: `jf_audiobooks\Brandon Sanderson\Isles\book.m4b`}, // rhapsode's
		},
		"other": {
			{Filename: `Music\Pink Floyd\Animals\01.flac`}, // someone else's
		},
	}

	got := s.ownedDownloads(downloads)
	if len(got) != 1 {
		t.Fatalf("want 1 owned user, got %d (%v)", len(got), got)
	}
	if ts := got["peer"]; len(ts) != 1 || ts[0].Filename != `Music\Toto\Toto IV\01 - Rosanna.flac` {
		t.Fatalf("want only dig's Toto transfer, got %v", ts)
	}
	if _, ok := got["other"]; ok {
		t.Fatalf("foreign user's transfers should be dropped, got %v", got["other"])
	}
}

// A nil cache (feature degraded) leaves the queue unfiltered rather than
// dropping every transfer.
func TestOwnedDownloadsNilCachePassesThrough(t *testing.T) {
	s := &Server{}
	downloads := map[string][]slskd.Transfer{"peer": {{Filename: `Music\A\B\1.flac`}}}
	got := s.ownedDownloads(downloads)
	if len(got["peer"]) != 1 {
		t.Fatalf("nil cache should pass transfers through, got %v", got)
	}
}

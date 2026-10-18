package web

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/Ebonhawk3829/dig/internal/notify"
	"github.com/Ebonhawk3829/dig/internal/slskd"
)

// dirState tracks one download directory's aggregate state between polls so
// the watcher can fire a notification exactly once per terminal transition.
type dirState struct {
	seen      bool
	notified  bool
	succeeded int
	failed    int
	active    int
	total     int
}

// WatchTransfers polls slskd's download queue in the background and sends a
// notification when a directory reaches a terminal state (all files done).
// It infers artist/album from the remote path. Runs until ctx is cancelled.
func (s *Server) WatchTransfers(ctx context.Context) {
	if s.notify == nil {
		return
	}
	states := make(map[string]*dirState) // key: username + "|" + albumDir
	firstPoll := true                    // don't notify for transfers that finished before startup

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		downloads, err := s.client.GetDownloads(ctx)
		if err != nil {
			continue // slskd unreachable; retry next tick silently
		}

		seen := make(map[string]bool)
		for user, transfers := range downloads {
			// group this user's latest transfers by album directory
			byDir := make(map[string][]slskd.Transfer)
			for _, t := range slskd.LatestTransfersPerFile(transfers) {
				dir := slskd.AlbumDir(t.Filename)
				byDir[dir] = append(byDir[dir], t)
			}
			for dir, ts := range byDir {
				key := user + "|" + dir
				seen[key] = true

				// The queue is shared with everything else on the slskd
				// instance, so only directories dig itself enqueued —
				// marked at download time — may notify. Unmarked transfers
				// are someone else's business; skip without touching
				// per-directory state so a later adoption by dig starts
				// clean rather than inheriting someone else's progress.
				own, err := s.cache.IsOwn(user, slskd.AlbumDirFromRemote(dir))
				if err != nil {
					log.Printf("watch: ownership check %s: %v", key, err)
					continue
				}
				if !own {
					continue
				}

				st := states[key]
				if st == nil {
					st = &dirState{}
					states[key] = st
				}

				succ, fail, active, total := slskd.AggregateStatus(ts)
				st.succeeded, st.failed, st.active, st.total = succ, fail, active, total

				// terminal: everything done, at least one file, not yet notified,
				// and not already finished when dig started up
				if active == 0 && total > 0 && !st.notified {
					if firstPoll {
						st.notified = true
						continue
					}
					st.notified = true
					artist, album := guessFromPath(dir)
					ev := notify.Event{
						Username: user,
						Artist:   artist,
						Album:    album,
						Detail:   detailString(succ, fail, total),
					}
					switch {
					case fail == 0:
						ev.State = "completed"
					case succ == 0:
						ev.State = "failed"
					default:
						ev.State = "partial"
					}
					log.Printf("notify: %s %s — %s (%s)", ev.State, ev.Album, ev.Detail, user)
					s.notify.Send(ev)
				}
			}
		}

		// forget directories that vanished from slskd (cleaned up)
		for k := range states {
			if !seen[k] {
				delete(states, k)
				// Drop the ownership mark too: the transfer left the queue
				// (cleared or finished-and-hidden) and the mark must not
				// accumulate forever.
				if parts := strings.SplitN(k, "|", 2); len(parts) == 2 {
					_ = s.cache.ForgetOwn(parts[0], slskd.AlbumDirFromRemote(parts[1]))
				}
			}
		}

		firstPoll = false // subsequent transitions are real events
	}
}

// guessFromPath extracts artist/album from a Soulseek path like
// "\\Music\\Toto\\Toto IV\\" — best effort, for notification text only.
func guessFromPath(dir string) (artist, album string) {
	parts := strings.Split(strings.ReplaceAll(dir, "\\", "/"), "/")
	var clean []string
	for _, p := range parts {
		if p != "" && p != "." {
			clean = append(clean, p)
		}
	}
	switch len(clean) {
	case 0:
		return "", ""
	case 1:
		return "", clean[0]
	default:
		return clean[len(clean)-2], clean[len(clean)-1]
	}
}

func detailString(succ, fail, total int) string {
	if fail > 0 {
		return itoa(succ) + "/" + itoa(total) + " files (" + itoa(fail) + " failed)"
	}
	return itoa(succ) + "/" + itoa(total) + " files"
}

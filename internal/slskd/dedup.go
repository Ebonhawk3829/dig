package slskd

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// InFlightGuard checks slskd's download queue to prevent duplicate enqueues
// (Layer 1 dedup, §6.1 of the proposal). No persistence — slskd is the source
// of truth.
type InFlightGuard struct {
	client *Client
}

// NewInFlightGuard creates a guard backed by the given client.
func NewInFlightGuard(c *Client) *InFlightGuard { return &InFlightGuard{client: c} }

// Check reports whether any enqueued or in-progress transfer already targets
// the same (username, album directory). It compares normalised album-level
// directories so "Disc 1" subdirs don't defeat the match.
func (g *InFlightGuard) Check(ctx context.Context, username, albumDir string) (bool, error) {
	downloads, err := g.client.GetDownloads(ctx)
	if err != nil {
		return false, fmt.Errorf("check in-flight transfers: %w", err)
	}

	want := normaliseDir(AlbumDirFromRemote(albumDir))
	for user, transfers := range downloads {
		if user != username {
			continue
		}
		for _, t := range LatestTransfersPerFile(transfers) {
			if IsTerminal(t) {
				continue
			}
			dir := normaliseDir(AlbumDir(t.Filename))
			if dir != "" && dir == want {
				return true, nil
			}
		}
	}
	return false, nil
}

// AlbumDirFromRemote normalises a remote directory string (backslash
// separated) into the same form AlbumDir produces.
func AlbumDirFromRemote(dir string) string {
	return strings.ReplaceAll(dir, "/", "\\")
}

func normaliseDir(dir string) string {
	return strings.ToLower(strings.Trim(strings.ReplaceAll(dir, "\\", "/"), "/"))
}

// LatestTransfersPerFile collapses slskd's one-record-per-retry-attempt list
// to the latest attempt per unique filename.
func LatestTransfersPerFile(transfers []Transfer) []Transfer {
	latest := make(map[string]Transfer, len(transfers))
	for _, t := range transfers {
		cur, ok := latest[t.Filename]
		if !ok || newer(t, cur) {
			latest[t.Filename] = t
		}
	}
	out := make([]Transfer, 0, len(latest))
	for _, t := range latest {
		out = append(out, t)
	}
	return out
}

func newer(a, b Transfer) bool {
	at := timestamp(a)
	bt := timestamp(b)
	if at != nil && bt != nil {
		return at.After(*bt)
	}
	return at != nil
}

func timestamp(t Transfer) *time.Time {
	if t.RequestedAt != nil {
		v := t.RequestedAt.Time
		return &v
	}
	if t.StartedAt != nil {
		v := t.StartedAt.Time
		return &v
	}
	return nil
}

// Terminal states as reported by slskd (comma-joined flags).
var terminalStates = map[string]bool{
	"succeeded": true, "errored": true, "cancelled": true,
	"rejected": true, "timedout": true, "aborted": true,
}

// IsTerminal reports whether a transfer's state is terminal.
func IsTerminal(t Transfer) bool {
	for _, flag := range strings.Split(strings.ToLower(t.State), ",") {
		flag = strings.TrimSpace(flag)
		if terminalStates[flag] {
			return true
		}
	}
	return false
}

// IsSucceeded reports whether the transfer completed successfully.
func IsSucceeded(t Transfer) bool {
	for _, flag := range strings.Split(strings.ToLower(t.State), ",") {
		if strings.TrimSpace(flag) == "succeeded" {
			return true
		}
	}
	return false
}

// AggregateStatus summarises the latest attempt per file for a candidate
// download: (succeeded, failed, active, total).
func AggregateStatus(transfers []Transfer) (succeeded, failed, active, total int) {
	latest := LatestTransfersPerFile(transfers)
	for _, t := range latest {
		total++
		switch {
		case IsSucceeded(t):
			succeeded++
		case IsTerminal(t):
			failed++
		default:
			active++
		}
	}
	return
}

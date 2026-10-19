package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Ebonhawk3829/dig/internal/slskd"
)

// handleStatusAPI serves a compact status summary for widgets/dashboards
// (Homepage, OliveTin, ntfy-adjacent tooling, etc.):
//
//	{"status":"ok","downloads":{"active":1,"completed_today":2,"speed_bps":5237796},
//	 "slskd":"ok","musicbrainz":"ok","library":"ok"}
//
// Read-only, no auth beyond the global basic-auth wrapper.
func (s *Server) handleStatusAPI(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r)
	defer cancel()

	type downloads struct {
		Active          int     `json:"active"`
		Failed          int     `json:"failed"`
		SpeedBps        float64 `json:"speed_bps"`
		FilesToday      int     `json:"files_completed_today"`
		ActiveDownloads []struct {
			Artist string  `json:"artist"`
			Album  string  `json:"album"`
			Percent float64 `json:"percent"`
			Speed  int64   `json:"speed_bps"`
		} `json:"active_downloads,omitempty"`
	}
	out := struct {
		Status      string    `json:"status"`
		Slskd       string    `json:"slskd"`
		MusicBrainz string    `json:"musicbrainz"`
		Library     string    `json:"library"`
		Now         time.Time `json:"now"`
		Downloads   downloads `json:"downloads"`
	}{Status: "ok", Now: time.Now().UTC()}

	// slskd + download summary
	downloadsData, err := s.client.GetDownloads(ctx)
	if err != nil {
		out.Slskd = "unreachable"
		out.Status = "degraded"
	} else {
		out.Slskd = "ok"
		// slskd's queue is shared with other tools (e.g. rhapsode), so only
		// credit transfers dig itself enqueued.
		downloadsData = s.ownedDownloads(downloadsData)
		dirs := transferDirs(downloadsData)
		midnight := time.Now().Truncate(24 * time.Hour)
		for _, d := range dirs {
			out.Downloads.Active += d.Active
			out.Downloads.Failed += d.Failed
			if d.Active > 0 {
				out.Downloads.SpeedBps += float64(d.Speed)
				out.Downloads.ActiveDownloads = append(out.Downloads.ActiveDownloads, struct {
					Artist  string  `json:"artist"`
					Album   string  `json:"album"`
					Percent float64 `json:"percent"`
					Speed   int64   `json:"speed_bps"`
				}{d.Artist, d.Album, d.Percent, d.Speed})
			}
			// count files whose transfers completed today (terminal state set
			// today); approximate via startedAt — good enough for a widget
			for _, t := range downloadsData[d.Username] {
				if slskd.IsSucceeded(t) && t.StartedAt != nil && t.StartedAt.After(midnight) {
					out.Downloads.FilesToday++
				}
			}
		}
	}

	// MusicBrainz
	if s.mb != nil {
		if err := s.mb.Ping(ctx); err != nil {
			out.MusicBrainz = "busy"
			if out.Status == "ok" {
				out.Status = "degraded"
			}
		} else {
			out.MusicBrainz = "ok"
		}
	} else {
		out.MusicBrainz = "not configured"
	}

	// Library
	if s.library != nil {
		if err := s.library.Ping(ctx); err != nil {
			out.Library = "unreachable"
			if out.Status == "ok" {
				out.Status = "degraded"
			}
		} else {
			out.Library = "ok"
		}
	} else {
		out.Library = "not configured"
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

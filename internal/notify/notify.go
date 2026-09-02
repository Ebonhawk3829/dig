// Package notify sends fire-and-forget JSON summaries on download terminal
// states (proposal §8.5). Any ntfy/Gotify/webhook-compatible endpoint works.
// Notification failures are logged, never surfaced as UI errors.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// Sender posts notifications to one webhook URL.
type Sender struct {
	url   string
	http  *http.Client
}

// New creates a sender. Returns nil if url is empty (notifications disabled).
func New(url string) *Sender {
	if url == "" {
		return nil
	}
	return &Sender{url: url, http: &http.Client{Timeout: 5 * time.Second}}
}

// Event describes a download terminal state.
type Event struct {
	State    string `json:"state"`              // "completed", "failed", "partial"
	Artist   string `json:"artist,omitempty"`
	Album    string `json:"album,omitempty"`
	Username string `json:"username,omitempty"`
	Detail   string `json:"detail,omitempty"`   // e.g. "9/12 files"
}

// Send posts the event. Fire-and-forget: errors are logged, never returned
// to the caller's flow. Runs in its own goroutine with a short timeout so a
// slow endpoint never blocks the UI.
func (s *Sender) Send(event Event) {
	if s == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		body, err := json.Marshal(event)
		if err != nil {
			log.Printf("notify: marshal: %v", err)
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
		if err != nil {
			log.Printf("notify: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		// ntfy convenience: title header renders nicely in the app
		req.Header.Set("X-Title", "dig: "+event.State)
		if event.Artist != "" && event.Album != "" {
			req.Header.Set("X-Title", "dig: "+event.Artist+" — "+event.Album+" "+event.State)
		}

		resp, err := s.http.Do(req)
		if err != nil {
			log.Printf("notify: %v", err)
			return
		}
		resp.Body.Close()
	}()
}

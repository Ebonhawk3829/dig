// Package library implements the optional Subsonic-API library check
// (proposal §6.1 Layer 2 dedup): a read-only "do I already have this?"
// warning. dig never writes to the library.
package library

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a Subsonic-compatible server (Navidrome, Airsonic, ...).
type Client struct {
	baseURL string
	user    string
	pass    string
	http    *http.Client
}

// New creates a Subsonic client. Returns nil if config is incomplete.
func New(baseURL, user, pass string) *Client {
	if baseURL == "" || user == "" {
		return nil
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		user:    user,
		pass:    pass,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// subsonicResponse is the common envelope of every Subsonic reply.
type subsonicResponse struct {
	SubsonicResponse struct {
		Status  string `json:"status"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
		SearchResult3 *struct {
			Album []struct {
				ID           string `json:"id"`
				Name         string `json:"name"`
				Artist       string `json:"artist"`
				Year         int    `json:"year"`
				SongCount    int    `json:"songCount"`
				Duration     int    `json:"duration"`
				AlbumCount   int    `json:"albumCount,omitempty"`
			} `json:"album"`
		} `json:"searchResult3,omitempty"`
	} `json:"subsonic-response"`
}

func (c *Client) get(ctx context.Context, endpoint string, params url.Values) (*subsonicResponse, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("u", c.user)
	params.Set("p", c.pass)
	params.Set("v", "1.16.1")
	params.Set("c", "dig")
	params.Set("f", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/rest/"+endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out subsonicResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("subsonic %s: decode: %w", endpoint, err)
	}
	r := out.SubsonicResponse
	if r.Status != "ok" {
		msg := "unknown error"
		if r.Error != nil {
			msg = r.Error.Message
		}
		return nil, fmt.Errorf("subsonic %s: %s", endpoint, msg)
	}
	return &out, nil
}

// AlbumMatch is an album found in the library.
type AlbumMatch struct {
	ID        string
	Name      string
	Artist    string
	Year      int
	SongCount int
	Duration  int // seconds
}

// FindAlbum searches the library for an album by artist + title. Matching is
// deliberately fuzzy (case-insensitive substring both ways) because library
// tags and MusicBrainz titles frequently disagree about punctuation.
func (c *Client) FindAlbum(ctx context.Context, artist, title string) ([]AlbumMatch, error) {
	q := strings.TrimSpace(title)
	if q == "" {
		return nil, nil
	}
	if artist != "" {
		q = artist + " " + q
	}
	out, err := c.get(ctx, "search3", url.Values{
		"query":      {q},
		"artistCount": {"1"},
		"albumCount":  {"20"},
	})
	if err != nil {
		return nil, err
	}

	var matches []AlbumMatch
	sr := out.SubsonicResponse.SearchResult3
	if sr == nil {
		return nil, nil
	}
	artistFold := strings.ToLower(strings.TrimSpace(artist))
	titleFold := strings.ToLower(strings.TrimSpace(title))

	for _, a := range sr.Album {
		nameFold := strings.ToLower(a.Name)
		artistMatch := artistFold == "" || strings.Contains(strings.ToLower(a.Artist), artistFold)
		titleMatch := titleFold == "" ||
			strings.Contains(nameFold, titleFold) ||
			strings.Contains(titleFold, nameFold)
		if artistMatch && titleMatch {
			matches = append(matches, AlbumMatch{
				ID: a.ID, Name: a.Name, Artist: a.Artist,
				Year: a.Year, SongCount: a.SongCount, Duration: a.Duration,
			})
		}
	}
	return matches, nil
}

// FindArtistAlbums returns the set of lowercased album names the library has
// for one artist — used to badge a whole discography grid in one request.
func (c *Client) FindArtistAlbums(ctx context.Context, artist string) (map[string]AlbumMatch, error) {
	if strings.TrimSpace(artist) == "" {
		return nil, nil
	}
	out, err := c.get(ctx, "search3", url.Values{
		"query":       {artist},
		"artistCount": {"1"},
		"albumCount":  {"500"},
	})
	if err != nil {
		return nil, err
	}
	result := make(map[string]AlbumMatch)
	sr := out.SubsonicResponse.SearchResult3
	if sr == nil {
		return result, nil
	}
	artistFold := strings.ToLower(strings.TrimSpace(artist))
	for _, a := range sr.Album {
		if !strings.Contains(strings.ToLower(a.Artist), artistFold) {
			continue
		}
		result[normaliseTitle(a.Name)] = AlbumMatch{
			ID: a.ID, Name: a.Name, Artist: a.Artist,
			Year: a.Year, SongCount: a.SongCount, Duration: a.Duration,
		}
	}
	return result, nil
}

// normaliseTitle folds a title for fuzzy map lookup: lowercase, strip
// punctuation and whitespace.
func normaliseTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// MatchInSet finds a discography entry in a FindArtistAlbums result set.
// Matching is containment-based on the normalised title (either direction)
// because library tags often carry edition suffixes the MB title lacks —
// e.g. library "Toto IV (picture disc)" vs MB "Toto IV".
func MatchInSet(set map[string]AlbumMatch, title string) (AlbumMatch, bool) {
	nt := normaliseTitle(title)
	if nt == "" {
		return AlbumMatch{}, false
	}
	// exact normalised hit first
	if m, ok := set[nt]; ok {
		return m, true
	}
	// containment: library title contains the MB title or vice versa
	for key, m := range set {
		if key == "" {
			continue
		}
		if strings.Contains(key, nt) || strings.Contains(nt, key) {
			return m, true
		}
	}
	return AlbumMatch{}, false
}

// Ping checks library reachability with a minimal authenticated request.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.get(ctx, "ping", nil)
	return err
}

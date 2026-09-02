package mb

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// ---- shared model types ----

type ArtistCredit struct {
	Name  string `json:"name"`
	Join  string `json:"joinphrase"`
	Artist struct {
		Name string `json:"name"`
	} `json:"artist"`
}

// FullName renders the credited artist name including join phrases.
func (a ArtistCredit) FullName() string {
	n := a.Name
	if n == "" {
		n = a.Artist.Name
	}
	return n + a.Join
}

type SecondaryType struct {
	// release-groups return secondary-types as a plain string array
}

type ReleaseGroup struct {
	ID             string         `json:"id"`
	Title          string         `json:"title"`
	PrimaryType    string         `json:"primary-type"`
	SecondaryTypes []string       `json:"secondary-types"`
	FirstRelease   string         `json:"first-release-date"`
	ArtistCredit   []ArtistCredit `json:"artist-credit"`
	Disambiguation string         `json:"disambiguation"`
}

type Medium struct {
	Format     string `json:"format"`
	TrackCount int    `json:"track-count"`
	Position   int    `json:"position"`
}

type Release struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Date       string         `json:"date"`
	Country    string         `json:"country"`
	Status     string         `json:"status"`
	Media      []Medium       `json:"media"`
	ArtistCredit []ArtistCredit `json:"artist-credit"`
}

type Track struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
	Title    string `json:"title"`
	Length   int64  `json:"length"` // ms
	Number   string `json:"number"`
}

type ReleaseDetail struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Date       string         `json:"date"`
	Country    string         `json:"country"`
	Media      []MediumDetail `json:"media"`
	ArtistCredit []ArtistCredit `json:"artist-credit"`
}

type MediumDetail struct {
	Format   string  `json:"format"`
	Position int     `json:"position"`
	Tracks   []Track `json:"tracks"`
}

// ---- search ----

type artistSearchResult struct {
	Artists []struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Disambiguation string `json:"disambiguation"`
		Type           string `json:"type"`
		Score          int    `json:"score"`
		Country        string `json:"country"`
	} `json:"artists"`
}

type rgSearchResult struct {
	ReleaseGroups []ReleaseGroup `json:"release-groups"`
}

// SearchArtists searches for artists by name.
func (c *Client) SearchArtists(ctx context.Context, q string, limit int) ([]ArtistHit, error) {
	var out artistSearchResult
	query := url.Values{}
	query.Set("query", fmt.Sprintf(`artist:"%s"^3 OR artistaccent:"%s"^3 OR alias:"%s"^2 OR %s`,
		EscapeLucene(q), EscapeLucene(q), EscapeLucene(q), EscapeLucene(q)))
	query.Set("limit", itoa(limit))
	query.Set("fmt", "json")
	if err := c.get(ctx, "/artist", query, c.ttlSearch, &out); err != nil {
		return nil, err
	}
	hits := make([]ArtistHit, 0, len(out.Artists))
	for _, a := range out.Artists {
		hits = append(hits, ArtistHit{
			ID: a.ID, Name: a.Name, Disambiguation: a.Disambiguation,
			Type: a.Type, Score: a.Score, Country: a.Country,
		})
	}
	return hits, nil
}

type ArtistHit struct {
	ID             string
	Name           string
	Disambiguation string
	Type           string
	Score          int
	Country        string
}

// SearchReleaseGroups searches for albums by title (optionally artist-scoped).
func (c *Client) SearchReleaseGroups(ctx context.Context, q, artist string, limit int) ([]ReleaseGroup, error) {
	var out rgSearchResult
	var query string
	if artist != "" {
		query = fmt.Sprintf(`(releasegroup:"%s"^3 OR release:"%s"^2) AND artist:"%s"`,
			EscapeLucene(q), EscapeLucene(q), EscapeLucene(artist))
	} else {
		query = fmt.Sprintf(`releasegroup:"%s"^3 OR release:"%s"^2 OR %s`,
			EscapeLucene(q), EscapeLucene(q), EscapeLucene(q))
	}
	v := url.Values{}
	v.Set("query", query)
	v.Set("limit", itoa(limit))
	v.Set("fmt", "json")
	if err := c.get(ctx, "/release-group", v, c.ttlSearch, &out); err != nil {
		return nil, err
	}
	return out.ReleaseGroups, nil
}

// ---- browse ----

// BrowseArtistReleaseGroups lists an artist's release-groups (paginated).
func (c *Client) BrowseArtistReleaseGroups(ctx context.Context, artistMBID string, limit, offset int) ([]ReleaseGroup, error) {
	var out struct {
		ReleaseGroups []ReleaseGroup `json:"release-groups"`
	}
	v := url.Values{}
	v.Set("artist", artistMBID)
	v.Set("inc", "artist-credits")
	v.Set("limit", itoa(limit))
	v.Set("offset", itoa(offset))
	v.Set("fmt", "json")
	if err := c.get(ctx, "/release-group", v, c.ttlDetail, &out); err != nil {
		return nil, err
	}
	return out.ReleaseGroups, nil
}

// GetReleaseGroup fetches one release-group with its releases (including
// per-release media so track counts are available for edition selection).
func (c *Client) GetReleaseGroup(ctx context.Context, rgid string) (*ReleaseGroupDetail, error) {
	var out ReleaseGroupDetail
	v := url.Values{}
	v.Set("inc", "artist-credits+releases+media")
	v.Set("fmt", "json")
	if err := c.get(ctx, "/release-group/"+rgid, v, c.ttlDetail, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type ReleaseGroupDetail struct {
	ID             string         `json:"id"`
	Title          string         `json:"title"`
	PrimaryType    string         `json:"primary-type"`
	SecondaryTypes []string       `json:"secondary-types"`
	FirstRelease   string         `json:"first-release-date"`
	ArtistCredit   []ArtistCredit `json:"artist-credit"`
	Disambiguation string         `json:"disambiguation"`
	Releases       []Release      `json:"releases"`
}

// GetRelease fetches one release with its tracklist.
func (c *Client) GetRelease(ctx context.Context, relid string) (*ReleaseDetail, error) {
	var out ReleaseDetail
	v := url.Values{}
	v.Set("inc", "recordings+labels")
	v.Set("fmt", "json")
	if err := c.get(ctx, "/release/"+relid, v, c.ttlDetail, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- filtering & edition selection (proposal §5.1) ----

// ExcludedSecondaryTypes are the release-group types hidden by default.
var ExcludedSecondaryTypes = map[string]bool{
	"compilation": true, "live": true, "remix": true, "soundtrack": true,
	"dj-mix": true, "mixtape/street": true, "demo": true,
}

// IsStudio reports whether a release-group has no excluded secondary types.
func IsStudio(rg ReleaseGroup) bool {
	for _, t := range rg.SecondaryTypes {
		if ExcludedSecondaryTypes[strings.ToLower(t)] {
			return false
		}
	}
	return true
}

// SelectEdition picks the best release in a group per the DN recall policy:
// closest track count -> Official -> earliest most-precise date -> XW -> MBID.
// targetTrackCount <= 0 means unknown (track-count term drops out).
func SelectEdition(releases []Release, targetTrackCount int) (string, *Release) {
	var best *Release
	var bestKey editionKey
	for i := range releases {
		r := &releases[i]
		if r.ID == "" {
			continue
		}
		tc := 0
		for _, m := range r.Media {
			tc += m.TrackCount
		}
		if tc == 0 {
			continue
		}
		k := editionKey{release: r}
		if targetTrackCount > 0 {
			d := tc - targetTrackCount
			if d < 0 {
				d = -d
			}
			k.trackDist = d
		}
		if !strings.EqualFold(r.Status, "Official") {
			k.statusPenalty = 1
		}
		k.date = EditionDateKey(r.Date)
		if r.Country != "XW" {
			k.countryPenalty = 1
		}
		k.mbid = r.ID
		if best == nil || lessEditionKey(k, bestKey) {
			best, bestKey = r, k
		}
	}
	if best == nil {
		return "", nil
	}
	return best.ID, best
}

type editionKey struct {
	trackDist      int
	statusPenalty  int
	date           [3]int
	countryPenalty int
	mbid           string
	release        *Release
}

func lessEditionKey(a, b editionKey) bool {
	if a.trackDist != b.trackDist {
		return a.trackDist < b.trackDist
	}
	if a.statusPenalty != b.statusPenalty {
		return a.statusPenalty < b.statusPenalty
	}
	for i := 0; i < 3; i++ {
		if a.date[i] != b.date[i] {
			return a.date[i] < b.date[i]
		}
	}
	if a.countryPenalty != b.countryPenalty {
		return a.countryPenalty < b.countryPenalty
	}
	return a.mbid < b.mbid
}

// EditionDateKey parses ISO dates with mixed precision into a sortable triple.
// Unknown components sort after known ones (sentinel 100); missing dates last.
func EditionDateKey(date string) [3]int {
	const unknown = 100
	var key [3]int
	if date == "" {
		return [3]int{10000, unknown, unknown}
	}
	parts := strings.Split(date, "-")
	for i := 0; i < 3; i++ {
		if i < len(parts) {
			n := 0
			ok := true
			for _, ch := range parts[i] {
				if ch < '0' || ch > '9' {
					ok = false
					break
				}
				n = n*10 + int(ch-'0')
			}
			if ok && len(parts[i]) > 0 {
				key[i] = n
				continue
			}
		}
		key[i] = unknown
	}
	return key
}

// EscapeLucene escapes Lucene reserved characters in a query phrase.
func EscapeLucene(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '+', '-', '&', '|', '!', '(', ')', '{', '}', '[', ']', '^', '"', '~', '*', '?', ':', '\\', '/':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ArtistName renders the primary credited artist for a release-group.
func ArtistName(credits []ArtistCredit) string {
	var b strings.Builder
	for _, c := range credits {
		b.WriteString(c.FullName())
	}
	return strings.TrimSpace(b.String())
}

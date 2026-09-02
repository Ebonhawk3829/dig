package slskd

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SearchOptions controls ladder execution.
type SearchOptions struct {
	TimeoutMS  int           // per-rung search timeout in ms
	Grace      time.Duration // extra wait past isComplete for late responses
	PollEvery  time.Duration // poll interval while waiting
	Progress   func(rung int, total int, query string) // optional progress callback
}

// RunSearch executes one query: start, poll until complete (+grace), fetch
// responses, then delete the search to keep slskd tidy.
func (c *Client) RunSearch(ctx context.Context, query string, opts SearchOptions) ([]UserResponse, error) {
	if opts.PollEvery == 0 {
		opts.PollEvery = 500 * time.Millisecond
	}
	if opts.Grace == 0 {
		opts.Grace = 5 * time.Second
	}

	id, err := c.StartSearch(ctx, query, opts.TimeoutMS)
	if err != nil {
		return nil, fmt.Errorf("start search %q: %w", query, err)
	}

	deadline := time.Now().Add(time.Duration(opts.TimeoutMS) * time.Millisecond)
	graceUntil := deadline.Add(opts.Grace)

	for {
		st, err := c.GetSearchState(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("poll search %q: %w", query, err)
		}
		if st.IsComplete || time.Now().After(graceUntil) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(opts.PollEvery):
		}
	}

	rs, err := c.GetSearchResponses(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("fetch responses %q: %w", query, err)
	}
	// slskd finalizes searches asynchronously; an immediate DELETE races with
	// finalization and logs DbUpdateConcurrencyException server-side. Delay
	// the cleanup and ignore failures — stale searches are pruned by slskd's
	// own retention settings anyway.
	go func(id string) {
		time.Sleep(30 * time.Second)
		_ = c.DeleteSearch(context.Background(), id)
	}(id)
	return rs, nil
}

// SanitizeQuery normalises a search term: strip space-surrounded hyphens,
// remove parentheses, normalise typographic apostrophes, collapse whitespace.
func SanitizeQuery(q string) string {
	q = strings.ReplaceAll(q, " - ", " ")
	q = strings.ReplaceAll(q, "’", "'")
	q = strings.ReplaceAll(q, "‘", "'")
	q = strings.ReplaceAll(q, "“", "\"")
	q = strings.ReplaceAll(q, "”", "\"")

	var b strings.Builder
	for _, r := range q {
		switch {
		case r == '(' || r == ')':
			// drop parentheses
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	// collapse whitespace
	fields := strings.Fields(out)
	out = strings.Join(fields, " ")
	if len(out) > 200 {
		out = out[:200]
	}
	return strings.TrimSpace(out)
}

// WildcardArtist replaces the first letter (and a trailing apostrophe) of each
// word with '*' — the DMCA-blocked-artist workaround. Words shorter than 3
// chars are left alone to avoid overly broad matches.
func WildcardArtist(artist string) string {
	words := strings.Fields(artist)
	out := make([]string, 0, len(words))
	for _, w := range words {
		out = append(out, wildcardWord(w))
	}
	return strings.Join(out, " ")
}

func wildcardWord(w string) string {
	runes := []rune(w)
	// strip a trailing apostrophe from consideration
	core := runes
	trailingApo := len(runes) > 0 && runes[len(runes)-1] == '\''
	if trailingApo {
		core = core[:len(core)-1]
	}
	if len(core) < 3 {
		return w // too short to wildcard safely
	}
	var b strings.Builder
	b.WriteRune('*')
	for _, r := range core[1:] {
		b.WriteRune(r)
	}
	if trailingApo {
		b.WriteRune('\'')
	}
	return b.String()
}

// BuildAlbumQueryLadder returns the 6-rung query ladder for an album: exact
// and wildcarded variants, most-specific first.
func BuildAlbumQueryLadder(artist, album string, year int) []string {
	a := SanitizeQuery(artist)
	al := SanitizeQuery(album)
	wc := WildcardArtist(a)

	var rungs []string
	if year > 0 {
		rungs = append(rungs,
			fmt.Sprintf("%s - %s - %d", a, al, year),
			fmt.Sprintf("%s - %s - %d", wc, al, year),
		)
	}
	rungs = append(rungs,
		fmt.Sprintf("%s - %s", a, al),
		fmt.Sprintf("%s - %s", wc, al),
		a,
		wc,
	)
	return dedupe(rungs)
}

func dedupe(qs []string) []string {
	seen := make(map[string]bool, len(qs))
	out := qs[:0]
	for _, q := range qs {
		q = strings.TrimSpace(q)
		if q == "" || seen[q] {
			continue
		}
		seen[q] = true
		out = append(out, q)
	}
	return out
}
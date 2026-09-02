package mb

import (
	"fmt"
	"net/http"
	"time"
)

// CoverArtURL returns the Cover Art Archive front-250 URL for a release
// group. CAA redirects to the actual image; browsers handle that natively so
// dig just links the redirect URL (no server-side fetch, no caching needed —
// the release-group endpoint persists across editions).
func CoverArtURL(rgid string) string {
	return fmt.Sprintf("https://coverartarchive.org/release-group/%s/front-250", rgid)
}

// caaReachable is a helper for tests/health checks (unused in the hot path —
// the browser fetches cover art directly from CAA).
var _ = http.MethodGet
var _ = time.Second

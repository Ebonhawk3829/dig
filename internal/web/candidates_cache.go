package web

import (
	"log"
	"strings"
	"time"

	"github.com/Ebonhawk3829/dig/internal/slskd"
)

// candidateCacheTTL is how long a scored candidate list stays re-openable
// without a fresh Soulseek search. It doubles as the lifetime of the
// download→search context used by "cancel & choose another".
const candidateCacheTTL = 24 * time.Hour

// cachedCandidates is the saved result of a candidate search, keyed by
// artist+album, so the page can be re-opened from the Transfers flow without
// re-running the (slow) query ladder.
type cachedCandidates struct {
	Artist        string
	Album         string
	ExpectedTrack int
	Candidates    []slskd.Scored
	SavedAt       time.Time
}

// downloadContext remembers which search a queued download came from. Keyed by
// (username, remote dir) so a cancel can jump back to that album's candidates.
type downloadContext struct {
	Artist string
	Album  string
}

// normKeyPart folds a query term to a stable cache-key component.
func normKeyPart(s string) string {
	return strings.ToLower(strings.TrimSpace(slskd.SanitizeQuery(s)))
}

func candidateCacheKey(artist, album string) string {
	return "cand:" + normKeyPart(artist) + "|" + normKeyPart(album)
}

func downloadContextKey(username, dir string) string {
	return "dlctx:" + username + "|" + slskd.AlbumDirFromRemote(dir)
}

func (s *Server) saveCandidates(artist, album string, expected int, cands []slskd.Scored) {
	if s.cache == nil {
		return
	}
	payload := cachedCandidates{
		Artist:        artist,
		Album:         album,
		ExpectedTrack: expected,
		Candidates:    cands,
		SavedAt:       time.Now(),
	}
	if err := s.cache.Set(candidateCacheKey(artist, album), payload, candidateCacheTTL); err != nil {
		log.Printf("cache candidates %s — %s: %v", artist, album, err)
	}
}

func (s *Server) loadCandidates(artist, album string) (cachedCandidates, bool) {
	if s.cache == nil {
		return cachedCandidates{}, false
	}
	var c cachedCandidates
	ok, err := s.cache.Get(candidateCacheKey(artist, album), &c)
	if err != nil {
		log.Printf("load candidates %s — %s: %v", artist, album, err)
		return cachedCandidates{}, false
	}
	return c, ok
}

func (s *Server) saveDownloadContext(username, dir, artist, album string) {
	if s.cache == nil || artist == "" || album == "" {
		return
	}
	if err := s.cache.Set(downloadContextKey(username, dir), downloadContext{Artist: artist, Album: album}, candidateCacheTTL); err != nil {
		log.Printf("cache download context %s/%s: %v", username, dir, err)
	}
}

func (s *Server) loadDownloadContext(username, dir string) (downloadContext, bool) {
	if s.cache == nil {
		return downloadContext{}, false
	}
	var c downloadContext
	ok, err := s.cache.Get(downloadContextKey(username, dir), &c)
	if err != nil {
		log.Printf("load download context %s/%s: %v", username, dir, err)
		return downloadContext{}, false
	}
	return c, ok
}

func (s *Server) forgetDownloadContext(username, dir string) {
	if s.cache == nil {
		return
	}
	if err := s.cache.Delete(downloadContextKey(username, dir)); err != nil {
		log.Printf("forget download context %s/%s: %v", username, dir, err)
	}
}

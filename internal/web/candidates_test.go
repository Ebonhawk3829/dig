package web

import (
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Ebonhawk3829/dig/internal/slskd"
	"github.com/Ebonhawk3829/dig/internal/store"
)

func testCache(t *testing.T) *store.Cache {
	t.Helper()
	c, err := store.Open(filepath.Join(t.TempDir(), "dig.db"))
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// The candidate list must come back out keyed by artist+album regardless of
// case/whitespace, so the Transfers "cancel & choose another" flow finds it.
func TestCandidateCacheRoundTripAndNormalisation(t *testing.T) {
	s := &Server{cache: testCache(t)}
	cands := []slskd.Scored{{
		Candidate: slskd.Candidate{
			Username:  "peer",
			ParentDir: `Music\Toto\Toto IV`,
			Files:     []slskd.File{{Filename: `Music\Toto\Toto IV\01 - Rosanna.flac`, Size: 123}},
		},
		Score: 0.9, Band: "accept",
	}}
	s.saveCandidates("Toto", "Toto IV", 11, cands)

	got, ok := s.loadCandidates("  TOTO ", "toto   iv")
	if !ok {
		t.Fatal("expected a cached candidate list")
	}
	if got.ExpectedTrack != 11 || len(got.Candidates) != 1 {
		t.Fatalf("cache round-trip mangled payload: %+v", got)
	}
	if got.Candidates[0].Username != "peer" || got.Candidates[0].Files[0].Size != 123 {
		t.Fatalf("candidate contents lost in round-trip: %+v", got.Candidates[0])
	}
}

func TestDownloadContextRoundTrip(t *testing.T) {
	s := &Server{cache: testCache(t)}
	s.saveDownloadContext("peer", `Music\Toto\Toto IV`, "Toto", "Toto IV")

	got, ok := s.loadDownloadContext("peer", `Music\Toto\Toto IV`)
	if !ok || got.Artist != "Toto" || got.Album != "Toto IV" {
		t.Fatalf("context round-trip: ok=%v got=%+v", ok, got)
	}

	s.forgetDownloadContext("peer", `Music\Toto\Toto IV`)
	if _, ok := s.loadDownloadContext("peer", `Music\Toto\Toto IV`); ok {
		t.Fatal("context should be gone after forget")
	}
}

// Progress is byte-weighted over the latest attempt per file, so it fills as
// bytes land and does not reset when the set of in-flight files changes.
func TestTransferDirsProgressByteWeighted(t *testing.T) {
	transfers := []slskd.Transfer{
		{Filename: `M\Toto\Toto IV\01.flac`, ID: "1", Size: 100, BytesTransferred: 100, State: "Completed, Succeeded"},
		{Filename: `M\Toto\Toto IV\02.flac`, ID: "2", Size: 100, BytesTransferred: 50, State: "InProgress"},
		{Filename: `M\Toto\Toto IV\03.flac`, ID: "3", Size: 100, BytesTransferred: 0, State: "Queued"},
	}
	rows := transferDirs(map[string][]slskd.Transfer{"peer": transfers})
	if len(rows) != 1 {
		t.Fatalf("want 1 dir row, got %d", len(rows))
	}
	r := rows[0]
	if r.Succeeded != 1 || r.Active != 2 {
		t.Fatalf("status counts wrong: succ=%d active=%d", r.Succeeded, r.Active)
	}
	if r.Percent < 49.9 || r.Percent > 50.1 {
		t.Fatalf("want ~50%% (150/300), got %.1f", r.Percent)
	}

	// More bytes arriving must not lower the bar.
	transfers[1].BytesTransferred = 90
	rows = transferDirs(map[string][]slskd.Transfer{"peer": transfers})
	if rows[0].Percent <= r.Percent {
		t.Fatalf("progress went backwards: %.1f -> %.1f", r.Percent, rows[0].Percent)
	}
}

// The cached banner must render (and offer a fresh re-search) without error.
func TestCandidatesTemplateRendersCachedBanner(t *testing.T) {
	tmpl, err := template.New("dig").Funcs(templateFuncs).ParseFS(templates, "templates/*.html")
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	var b strings.Builder
	err = tmpl.ExecuteTemplate(&b, "candidates.html", candidatesData{
		Artist: "Toto", Album: "Toto IV", Cached: true, SavedAt: "09:41",
		Candidates: []slskd.Scored{{
			Candidate:   slskd.Candidate{Username: "peer", ParentDir: `Music\Toto\Toto IV`},
			Band:        "accept",
			Score:       0.9,
			FormatLabel: "FLAC",
		}},
	})
	if err != nil {
		t.Fatalf("execute candidates template: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "Showing saved results") || !strings.Contains(out, "Re-search") {
		t.Fatalf("cached banner missing from output:\n%s", out)
	}
	if !strings.Contains(out, "artist=Toto") || !strings.Contains(out, "album=Toto") {
		t.Fatalf("re-search link missing artist/album:\n%s", out)
	}
}

// handleTransferCancel must cancel the directory's records, drop dig's marks,
// and redirect to the album's cached candidate list.
func TestHandleTransferCancelRedirectsToCachedCandidates(t *testing.T) {
	cache := testCache(t)
	dir := `Music\Toto\Toto IV`
	if err := cache.MarkOwn("peer", slskd.AlbumDirFromRemote(dir)); err != nil {
		t.Fatalf("mark own: %v", err)
	}

	var mu sync.Mutex
	deletes := 0
	slskdSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v0/transfers/downloads":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w,
				`[{"username":"peer","directories":[{"directory":"Music\\Toto\\Toto IV",`+
					`"files":[{"id":"11111111-1111-1111-1111-111111111111",`+
					`"filename":"Music\\Toto\\Toto IV\\01 - Rosanna.flac","size":10,"state":"InProgress"}]}]}]`)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v0/transfers/downloads/"):
			mu.Lock()
			deletes++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer slskdSrv.Close()

	s := &Server{client: slskd.New(slskdSrv.URL, "key"), cache: cache}
	s.saveDownloadContext("peer", dir, "Toto", "Toto IV")

	form := url.Values{"username": {"peer"}, "dir": {dir}, "artist": {"ignored"}, "album": {"ignored"}}
	req := httptest.NewRequest(http.MethodPost, "/transfers/cancel", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransferCancel(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/candidates?") || !strings.Contains(loc, "from=cache") {
		t.Fatalf("expected redirect to cached candidates, got %q", loc)
	}
	// The recorded context (not the form) supplies the real search terms.
	if !strings.Contains(loc, "artist=Toto") || !strings.Contains(loc, "album=Toto+IV") {
		t.Fatalf("redirect should carry the recorded artist/album, got %q", loc)
	}

	mu.Lock()
	gotDeletes := deletes
	mu.Unlock()
	if gotDeletes == 0 {
		t.Fatal("expected at least one cancel DELETE against slskd")
	}

	if own, _ := cache.IsOwn("peer", slskd.AlbumDirFromRemote(dir)); own {
		t.Fatal("ownership mark should be cleared on cancel")
	}
	if _, ok := s.loadDownloadContext("peer", dir); ok {
		t.Fatal("download context should be cleared on cancel")
	}
}

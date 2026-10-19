// Package web serves dig's server-rendered UI.
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ebonhawk3829/dig/internal/config"
	"github.com/Ebonhawk3829/dig/internal/library"
	"github.com/Ebonhawk3829/dig/internal/mb"
	"github.com/Ebonhawk3829/dig/internal/notify"
	"github.com/Ebonhawk3829/dig/internal/slskd"
	"github.com/Ebonhawk3829/dig/internal/store"
)

// Server holds the web server's dependencies.
type Server struct {
	cfg     config.Config
	client  *slskd.Client
	guard   *slskd.InFlightGuard
	mb      *mb.Client
	cache   *store.Cache
	library *library.Client
	notify  *notify.Sender
	tmpl    *template.Template
}

// New creates the web server. mbClient, cache, lib and notifier may be nil
// (those features degrade gracefully; manual search still works).
func New(cfg config.Config, client *slskd.Client, guard *slskd.InFlightGuard, mbClient *mb.Client, cache *store.Cache, lib *library.Client, notifier *notify.Sender) (*Server, error) {
	t, err := template.New("dig").Funcs(templateFuncs).ParseFS(templates, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, client: client, guard: guard, mb: mbClient, cache: cache, library: lib, notify: notifier, tmpl: t}
	// Apply persisted UI overrides (Settings page) on top of env/YAML config.
	// Without this, saved settings are lost on every container restart.
	s.rebuildClients()
	return s, nil
}

// Handler returns the root http.Handler with all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", staticHandler())
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("POST /search", s.handleSearch)
	mux.HandleFunc("GET /results", s.handleResults)
	mux.HandleFunc("GET /artist/{id}", s.handleArtist)
	mux.HandleFunc("GET /album/{id}", s.handleAlbum)
	mux.HandleFunc("GET /candidates", s.handleCandidates)
	mux.HandleFunc("POST /download", s.handleDownload)
	mux.HandleFunc("GET /transfers", s.handleTransfers)
	mux.HandleFunc("GET /api/transfers", s.handleTransfersAPI)
	mux.HandleFunc("POST /transfers/retry", s.handleTransferRetry)
	mux.HandleFunc("POST /transfers/clear", s.handleTransferClear)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings", s.handleSettingsSave)
	mux.HandleFunc("POST /settings/test", s.handleSettingsTest)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/status", s.handleStatusAPI)
	return s.withAuth(mux)
}

// withAuth applies optional basic auth.
func (s *Server) withAuth(next http.Handler) http.Handler {
	u, p := s.cfg.Server.Auth.Username, s.cfg.Server.Auth.Password
	if u == "" || p == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != u || pass != p {
			w.Header().Set("WWW-Authenticate", `Basic realm="dig"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	health := struct {
		Status  string `json:"status"`
		Slskd   string `json:"slskd"`
		MusicBrainz string `json:"musicbrainz"`
		Library string `json:"library,omitempty"`
	}{Status: "ok", Slskd: "unknown", MusicBrainz: "unknown", Library: "not configured"}

	// slskd: any response (even an auth error) means reachable
	if _, err := s.client.GetDownloads(ctx); err != nil {
		health.Slskd = "unreachable: " + err.Error()
		health.Status = "degraded"
	} else {
		health.Slskd = "ok"
	}

	// MusicBrainz: cheap ping
	if s.mb != nil {
		if err := s.mb.Ping(ctx); err != nil {
			health.MusicBrainz = "unreachable: " + err.Error()
		} else {
			health.MusicBrainz = "ok"
		}
	} else {
		health.MusicBrainz = "not configured"
	}

	// Library: ping when configured
	if s.library != nil {
		if err := s.library.Ping(ctx); err != nil {
			health.Library = "unreachable: " + err.Error()
		} else {
			health.Library = "ok"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(health)
}

// ---- Home / search ----

type homeData struct {
	Query  string
	Artist string
	Album  string
	Error  string
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	s.render(w, "home.html", homeData{})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("q"))
	artist := strings.TrimSpace(r.FormValue("artist"))
	album := strings.TrimSpace(r.FormValue("album"))
	if q == "" && artist == "" && album == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	// Split mode (artist and/or album) takes precedence over free-text.
	if artist != "" || album != "" {
		http.Redirect(w, r,
			"/results?artist="+queryEscape(artist)+"&album="+queryEscape(album),
			http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/results?q="+queryEscape(q), http.StatusSeeOther)
}

// ---- Results (Phase 2: MusicBrainz search with manual fallback) ----

type artistHitView struct {
	ID             string
	Name           string
	Disambiguation string
	Type           string
}

type albumHitView struct {
	ID           string
	Title        string
	Artist       string
	Year         string
	PrimaryType  string
	CoverURL     string
	InLibrary    bool
	LibInfo      string
}

type resultsData struct {
	Query      string // free-text query (single-field mode)
	Artist     string // artist field (split mode)
	Album      string // album field (split mode)
	Artists    []artistHitView
	Albums     []albumHitView
	MBError    string // set when MusicBrainz is unreachable
	ManualHint bool
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	artist := strings.TrimSpace(r.URL.Query().Get("artist"))
	album := strings.TrimSpace(r.URL.Query().Get("album"))
	if q == "" && artist == "" && album == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	data := resultsData{Query: q, Artist: artist, Album: album}

	if s.mb == nil {
		data.ManualHint = true
		s.render(w, "results.html", data)
		return
	}

	ctx, cancel := contextWithTimeout(r)
	defer cancel()

	// Artist search: free-text q, or the artist field in split mode.
	artistTerm := q
	if artist != "" {
		artistTerm = artist
	}
	if artistTerm != "" {
		var artists []mb.ArtistHit
		err := mb.WithRetry(ctx, func() error {
			var e error
			artists, e = s.mb.SearchArtists(ctx, artistTerm, 10)
			return e
		})
		if err != nil {
			data.MBError = mbUnavailableMessage(err)
			data.ManualHint = true
			s.render(w, "results.html", data)
			return
		}
		for _, a := range artists {
			data.Artists = append(data.Artists, artistHitView{
				ID: a.ID, Name: a.Name, Disambiguation: a.Disambiguation, Type: a.Type,
			})
		}
	}

	// Album search: free-text q, or album (+artist scope) in split mode.
	albumTerm := q
	albumArtist := ""
	if album != "" {
		albumTerm = album
		albumArtist = artist // scope album search to the artist when both given
	}
	if albumTerm != "" {
		var rgs []mb.ReleaseGroup
		err := mb.WithRetry(ctx, func() error {
			var e error
			rgs, e = s.mb.SearchReleaseGroups(ctx, albumTerm, albumArtist, 12)
			return e
		})
		if err != nil {
			data.MBError = mbUnavailableMessage(err)
		} else {
			for _, rg := range rgs {
				data.Albums = append(data.Albums, albumHitView{
					ID:          rg.ID,
					Title:       rg.Title,
					Artist:      mb.ArtistName(rg.ArtistCredit),
					Year:        firstYear(rg.FirstRelease),
					PrimaryType: rg.PrimaryType,
					CoverURL:    mb.CoverArtURL(rg.ID),
				})
			}
			// Layer 2 dedup on results: badge albums already in the library,
			// grouped per artist so each artist costs one query.
			if s.library != nil && len(data.Albums) > 0 {
				byArtist := make(map[string][]int)
				for i, a := range data.Albums {
					if a.Artist != "" {
						byArtist[a.Artist] = append(byArtist[a.Artist], i)
					}
				}
				for artist, idxs := range byArtist {
					libCtx, libCancel := context.WithTimeout(ctx, 8*time.Second)
					set, err := s.library.FindArtistAlbums(libCtx, artist)
					libCancel()
					if err != nil {
						continue
					}
					for _, i := range idxs {
						if m, ok := library.MatchInSet(set, data.Albums[i].Title); ok {
							data.Albums[i].InLibrary = true
							data.Albums[i].LibInfo = libInfo(m)
						}
					}
				}
			}
		}
	}
	s.render(w, "results.html", data)
}

func mbUnavailableMessage(err error) string {
	return "MusicBrainz is unreachable (" + err.Error() + ") — use manual search below."
}

// mbUnavailableShort is for pages without a manual-search form.
func mbUnavailableShort(err error) string {
	return "MusicBrainz is unreachable (" + err.Error() + ")."
}

// ---- Artist discography ----

type discographyView struct {
	ID          string
	Title       string
	Year        string
	PrimaryType string
	CoverURL    string
	Studio      bool
	InLibrary   bool
	LibInfo     string // e.g. "11 tracks · 2011" from the library match
}

type artistData struct {
	ID             string
	Name           string
	Disambiguation string
	Albums         []discographyView
	MBError        string
}

func (s *Server) handleArtist(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.mb == nil {
		s.render(w, "error.html", map[string]string{"Error": mbUnavailableShort(errMBDisabled)})
		return
	}
	ctx, cancel := contextWithTimeout(r)
	defer cancel()

	var rgs []mb.ReleaseGroup
	err := mb.WithRetry(ctx, func() error {
		var e error
		rgs, e = s.mb.BrowseArtistReleaseGroups(ctx, id, 100, 0)
		return e
	})
	if err != nil {
		s.render(w, "error.html", map[string]string{"Error": mbUnavailableShort(err)})
		return
	}
	data := artistData{ID: id}
	for _, rg := range rgs {
		studio := mb.IsStudio(rg)
		data.Albums = append(data.Albums, discographyView{
			ID:          rg.ID,
			Title:       rg.Title,
			Year:        firstYear(rg.FirstRelease),
			PrimaryType: rg.PrimaryType,
			CoverURL:    mb.CoverArtURL(rg.ID),
			Studio:      studio,
		})
		if data.Name == "" && len(rg.ArtistCredit) > 0 {
			data.Name = mb.ArtistName(rg.ArtistCredit)
		}
	}

	// Layer 2 dedup: badge albums already in the library (one Subsonic query
	// for the whole grid; failures are silent — advisory only).
	if s.library != nil && data.Name != "" {
		libCtx, libCancel := context.WithTimeout(ctx, 8*time.Second)
		if set, err := s.library.FindArtistAlbums(libCtx, data.Name); err == nil {
			for i := range data.Albums {
				if m, ok := library.MatchInSet(set, data.Albums[i].Title); ok {
					data.Albums[i].InLibrary = true
					data.Albums[i].LibInfo = libInfo(m)
				}
			}
		}
		libCancel()
	}

	s.render(w, "artist.html", data)
}

// libInfo renders a short "what's on disk" summary for a library match.
// Year deliberately omitted — it's already shown from the MB metadata and a
// mismatched year invites confusion.
func libInfo(m library.AlbumMatch) string {
	if m.SongCount > 0 {
		return fmt.Sprintf("%d tracks", m.SongCount)
	}
	return "in library"
}

// ---- Album detail ----

type trackView struct {
	Position string
	Title    string
	Duration string
}

type editionView struct {
	ID       string
	Label    string
	Selected bool
}

type albumData struct {
	RGID          string
	ReleaseID     string
	Title         string
	Artist        string
	ArtistID      string
	Year          string
	PrimaryType   string
	SecondaryTypes []string
	CoverURL      string
	Disambiguation string
	Tracks        []trackView
	Editions      []editionView
	ExpectedTracks int
	MBError       string
	InLibrary     bool
	LibInfo       string
}

func (s *Server) handleAlbum(w http.ResponseWriter, r *http.Request) {
	rgid := r.PathValue("id")
	requestedEdition := strings.TrimSpace(r.URL.Query().Get("edition"))
	if s.mb == nil {
		s.render(w, "error.html", map[string]string{"Error": mbUnavailableShort(errMBDisabled)})
		return
	}
	ctx, cancel := contextWithTimeout(r)
	defer cancel()

	rg, err := s.mb.GetReleaseGroup(ctx, rgid)
	if err != nil {
		s.render(w, "error.html", map[string]string{"Error": mbUnavailableShort(err)})
		return
	}

	data := albumData{
		RGID:           rg.ID,
		Title:          rg.Title,
		Artist:         mb.ArtistName(rg.ArtistCredit),
		Year:           firstYear(rg.FirstRelease),
		PrimaryType:    rg.PrimaryType,
		SecondaryTypes: rg.SecondaryTypes,
		CoverURL:       mb.CoverArtURL(rg.ID),
		Disambiguation: rg.Disambiguation,
	}
	if len(rg.ArtistCredit) > 0 && rg.ArtistCredit[0].Artist.Name != "" {
		// artist page link needs the artist MBID; fetch from first release credit
		_ = rg.ArtistCredit[0]
	}

	// pick best edition (target track count unknown at group level; use the
	// median track count across releases as the target)
	relID, rel := mb.SelectEdition(rg.Releases, 0)
	if requestedEdition != "" {
		// operator picked a specific edition from the dropdown
		for i := range rg.Releases {
			if rg.Releases[i].ID == requestedEdition {
				relID, rel = requestedEdition, &rg.Releases[i]
				break
			}
		}
	}
	if relID == "" {
		data.MBError = "No usable editions found for this release group."
		s.render(w, "album.html", data)
		return
	}
	data.ReleaseID = relID

	// target track count = the selected edition's count; re-select with it so
	// the ranking matches the proposal's policy (only when no explicit edition)
	tc := 0
	for _, m := range rel.Media {
		tc += m.TrackCount
	}
	data.ExpectedTracks = tc
	if tc > 0 && requestedEdition == "" {
		if _, rel2 := mb.SelectEdition(rg.Releases, tc); rel2 != nil {
			rel = rel2
			data.ReleaseID = rel2.ID
		}
	}

	// edition dropdown
	seen := map[string]bool{}
	for i := range rg.Releases {
		rel := &rg.Releases[i]
		if rel.ID == "" || seen[rel.ID] {
			continue
		}
		seen[rel.ID] = true
		label := strings.TrimSpace(rel.Date + " " + rel.Country + " " + rel.Status)
		if label == "" {
			label = "edition"
		}
		data.Editions = append(data.Editions, editionView{
			ID: rel.ID, Label: label, Selected: rel.ID == data.ReleaseID,
		})
	}

	// tracklist from the selected release
	var detail *mb.ReleaseDetail
	err2 := mb.WithRetry(ctx, func() error {
		var e error
		detail, e = s.mb.GetRelease(ctx, data.ReleaseID)
		return e
	})
	if err2 != nil {
		data.MBError = mbUnavailableShort(err2)
	} else {
		for _, m := range detail.Media {
			for _, t := range m.Tracks {
				pos := t.Number
				if pos == "" {
					pos = itoa(t.Position)
				}
				data.Tracks = append(data.Tracks, trackView{
					Position: pos,
					Title:    t.Title,
					Duration: formatMS(t.Length),
				})
			}
		}
	}

	// Layer 2 dedup: "you potentially have this already" banner (advisory,
	// dismissible by just clicking Find on Soulseek anyway).
	if s.library != nil && data.Title != "" {
		libCtx, libCancel := context.WithTimeout(ctx, 8*time.Second)
		if matches, err := s.library.FindAlbum(libCtx, data.Artist, data.Title); err == nil && len(matches) > 0 {
			data.InLibrary = true
			data.LibInfo = libInfo(matches[0])
		}
		libCancel()
	}

	s.render(w, "album.html", data)
}

var errMBDisabled = fmt.Errorf("metadata source not configured")

func firstYear(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return date
}

func formatMS(ms int64) string {
	if ms <= 0 {
		return ""
	}
	secs := ms / 1000
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

func contextWithTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 30*time.Second)
}



// flushLoading writes the loading shell for the candidates page and flushes
// it to the browser. Safe no-op when the ResponseWriter doesn't flush.
func flushLoading(w http.ResponseWriter, artist, album string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if f, ok := w.(http.Flusher); ok {
		if err := s_renderLoading(w, artist, album); err != nil {
			log.Printf("render loading: %v", err)
		}
		f.Flush()
	}
}

func s_renderLoading(w http.ResponseWriter, artist, album string) error {
	return loadingTemplate.Execute(w, loadingData{Artist: artist, Album: album})
}

type loadingData struct {
	Artist string
	Album  string
}

var loadingTemplate = template.Must(template.New("loading").Funcs(templateFuncs).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <title>dig — searching</title>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="icon" type="image/png" sizes="32x32" href="/static/img/favicon-32.png">
  <link rel="stylesheet" href="/static/theme.css">
  <style>
    .loading { display:flex; flex-direction:column; align-items:center; gap:1rem; margin:4rem 0; }
    .spinner { width:42px; height:42px; border:4px solid var(--border); border-top-color:var(--teal);
               border-radius:50%; animation:spin 1s linear infinite; }
    @keyframes spin { to { transform:rotate(360deg); } }
  </style>
</head>
<body>
<main>
  <h1><a href="/"><img class="logo" src="/static/img/logo.png" alt="dig logo" style="width:28px;height:28px;vertical-align:middle"> dig</a></h1>
  <div class="loading">
    <div class="spinner"></div>
    <div><strong>Digging…</strong></div>
    <div class="muted">Searching Soulseek for {{.Artist}} — {{.Album}}.</div>
    <div class="muted">This can take up to a minute on slow rungs.</div>
  </div>
</main>
</body>
</html>`))

// ---- Candidates ----

type candidatesData struct {
	Artist        string
	Album         string
	ExpectedTrack int
	Candidates    []slskd.Scored
	Error         string
	Searching     bool
}

func (s *Server) handleCandidates(w http.ResponseWriter, r *http.Request) {
	artist := strings.TrimSpace(r.URL.Query().Get("artist"))
	album := strings.TrimSpace(r.URL.Query().Get("album"))
	if artist == "" || album == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	ctx := r.Context()

	// Stream a loading page immediately: the ladder can take ~90s worst case
	// and an instant "searching" screen beats a blank wait. The rest of the
	// response (results or error) is streamed after the search completes.
	flushLoading(w, artist, album)
	opts := slskd.SearchOptions{
		TimeoutMS: s.cfg.Slskd.SearchTimeoutMS,
		Grace:     5 * time.Second,
	}

	// Expected track count: query param (from album page) wins; else resolve
	// from MusicBrainz when available so completeness scoring works; 0
	// (unknown) degrades gracefully.
	expectedTracks := 0
	if v := r.URL.Query().Get("tracks"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			expectedTracks = n
		}
	}
	if expectedTracks == 0 && s.mb != nil {
		mbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if rgs, err := s.mb.SearchReleaseGroups(mbCtx, album, artist, 5); err == nil && len(rgs) > 0 {
			if rg, err := s.mb.GetReleaseGroup(mbCtx, rgs[0].ID); err == nil {
				if _, rel := mb.SelectEdition(rg.Releases, 0); rel != nil {
					for _, m := range rel.Media {
						expectedTracks += m.TrackCount
					}
				}
			}
		}
		cancel()
	}

	ladder := slskd.BuildAlbumQueryLadder(artist, album, 0)

	var all []slskd.UserResponse
	for i, q := range ladder {
		if opts.Progress != nil {
			opts.Progress(i+1, len(ladder), q)
		}
		rs, err := s.client.RunSearch(ctx, q, opts)
		if err != nil {
			s.render(w, "candidates.html", candidatesData{
				Artist: artist, Album: album, Error: err.Error(),
			})
			return
		}
		all = append(all, rs...)
		if hasMeaningfulResults(rs) {
			break // stop at the first rung that returns results
		}
	}

	grouped := slskd.GroupResponses(all)
	scored := slskd.ScoreCandidates(grouped, artist, album, expectedTracks,
		slskd.ScoreWeights{
			Completeness: s.cfg.Scoring.WeightCompleteness,
			Dirname:      s.cfg.Scoring.WeightDirname,
			Format:       s.cfg.Scoring.WeightFormat,
			Source:       s.cfg.Scoring.WeightSource,
		},
		s.cfg.Scoring.AcceptThreshold, s.cfg.Scoring.RejectThreshold)

	s.render(w, "candidates.html", candidatesData{
		Artist: artist, Album: album, Candidates: scored, ExpectedTrack: expectedTracks,
	})
}

func hasMeaningfulResults(rs []slskd.UserResponse) bool {
	for _, r := range rs {
		if len(r.Files) > 0 {
			return true
		}
	}
	return false
}

// ---- Download ----

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	dir := r.FormValue("dir")
	artist := r.FormValue("artist")
	album := r.FormValue("album")
	if username == "" || dir == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	ctx := r.Context()

	// Layer 1 dedup: block if already downloading from this source.
	inFlight, err := s.guard.Check(ctx, username, dir)
	if err != nil {
		s.render(w, "error.html", map[string]string{"Error": err.Error()})
		return
	}
	if inFlight {
		s.render(w, "error.html", map[string]string{
			"Error": "Already downloading from this source (" + username + "). See Transfer Status.",
		})
		return
	}

	// Re-run the ladder to find the files for this (username, dir). Phase 1
	// keeps this simple: search again and enqueue the matching folder.
	files, err := s.findFilesFor(ctx, username, dir, artist, album)
	if err != nil {
		s.render(w, "error.html", map[string]string{"Error": err.Error()})
		return
	}
	if len(files) == 0 {
		s.render(w, "error.html", map[string]string{
			"Error": "No files found for the selected candidate — the peer's share may have changed.",
		})
		return
	}

	reqs := make([]slskd.EnqueueRequest, len(files))
	for i, f := range files {
		reqs[i] = slskd.EnqueueRequest{Filename: f.Filename, Size: f.Size}
	}
	if _, err := s.client.Enqueue(ctx, username, reqs); err != nil {
		s.render(w, "error.html", map[string]string{"Error": err.Error()})
		return
	}
	// Mark before redirecting: the watcher consults this to separate dig's
	// transfers from anything else sharing the slskd account.
	if err := s.cache.MarkOwn(username, slskd.AlbumDirFromRemote(dir)); err != nil {
		log.Printf("mark own %s/%s: %v", username, dir, err)
	}
	http.Redirect(w, r, "/transfers", http.StatusSeeOther)
}

// findFilesFor re-runs the query ladder and returns the files belonging to
// (username, dir).
func (s *Server) findFilesFor(ctx interface {
	Done() <-chan struct{}
	Err() error
	Deadline() (time.Time, bool)
	Value(any) any
}, username, dir, artist, album string) ([]slskd.File, error) {
	opts := slskd.SearchOptions{TimeoutMS: s.cfg.Slskd.SearchTimeoutMS, Grace: 5 * time.Second}
	ladder := slskd.BuildAlbumQueryLadder(artist, album, 0)
	for _, q := range ladder {
		rs, err := s.client.RunSearch(ctx, q, opts)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			if r.Username != username {
				continue
			}
			var files []slskd.File
			for _, f := range r.Files {
				if slskd.AlbumDir(f.Filename) == dir {
					files = append(files, f)
				}
			}
			if len(files) > 0 {
				return files, nil
			}
		}
		if hasMeaningfulResults(rs) {
			break
		}
	}
	return nil, nil
}

// ---- Transfers ----

type dirRow struct {
	Username  string  `json:"username"`
	Dir       string  `json:"dir"`
	Artist    string  `json:"artist"`
	Album     string  `json:"album"`
	Percent   float64 `json:"percent"`
	Succeeded int     `json:"succeeded"`
	Failed    int     `json:"failed"`
	Active    int     `json:"active"`
	Total     int     `json:"total"`
	FailState string  `json:"failState"`
	Speed     int64   `json:"speed"` // bytes/sec across active files
}

type transfersData struct {
	Dirs []dirRow
}

// ownedDownloads narrows slskd's shared download queue to directories dig
// itself enqueued. The slskd account is shared with other tools (notably
// rhapsode), so status and transfer views must not credit their transfers to
// dig. Ownership marks are written at enqueue time (handleDownload,
// handleTransferRetry) and the notification watcher already consults them; the
// read paths reuse the same marks. A nil cache leaves the queue unfiltered.
func (s *Server) ownedDownloads(downloads map[string][]slskd.Transfer) map[string][]slskd.Transfer {
	if s.cache == nil {
		return downloads
	}
	out := make(map[string][]slskd.Transfer, len(downloads))
	for user, transfers := range downloads {
		for _, t := range transfers {
			own, err := s.cache.IsOwn(user, slskd.AlbumDirFromRemote(slskd.AlbumDir(t.Filename)))
			if err != nil {
				log.Printf("owned downloads %s/%s: %v", user, t.Filename, err)
				continue
			}
			if own {
				out[user] = append(out[user], t)
			}
		}
	}
	return out
}

// transferDirs groups slskd's download tree into per-directory summary rows.
func transferDirs(downloads map[string][]slskd.Transfer) []dirRow {
	type key struct{ user, dir string }
	groups := make(map[key]*[]slskd.Transfer)
	for user, transfers := range downloads {
		for _, t := range transfers {
			k := key{user, slskd.AlbumDir(t.Filename)}
			if groups[k] == nil {
				g := []slskd.Transfer{t}
				groups[k] = &g
			} else {
				*groups[k] = append(*groups[k], t)
			}
		}
	}
	rows := make([]dirRow, 0, len(groups))
	for k, ts := range groups {
		succ, fail, active, total := slskd.AggregateStatus(*ts)
		percent := 0.0
		if total > 0 {
			percent = float64(succ) / float64(total) * 100
		}
		// live progress: for in-flight dirs, average byte progress beats
		// succeeded-count percentage
		if active > 0 {
			var bytes, sizes int64
			for _, t := range slskd.LatestTransfersPerFile(*ts) {
				if !slskd.IsTerminal(t) {
					bytes += t.BytesTransferred
					sizes += t.Size
				}
			}
			if sizes > 0 {
				percent = float64(bytes) / float64(sizes) * 100
			}
		}
		artist, album := guessFromPath(k.dir)
		row := dirRow{
			Username: k.user, Dir: k.dir, Artist: artist, Album: album,
			Percent: percent, Succeeded: succ, Failed: fail, Active: active, Total: total,
		}
		row.Speed = liveSpeed(*ts)
		for _, t := range *ts {
			if slskd.IsTerminal(t) && !slskd.IsSucceeded(t) {
				row.FailState = t.State
				break
			}
		}
		rows = append(rows, row)
	}
	// active first, then failed, then completed; newest activity first
	sort.Slice(rows, func(i, j int) bool {
		ai, aj := rows[i].Active > 0, rows[j].Active > 0
		if ai != aj {
			return ai
		}
		return rows[i].Percent < rows[j].Percent
	})
	return rows
}

// liveSpeed sums the average speed of active transfers (bytes/sec).
func liveSpeed(transfers []slskd.Transfer) int64 {
	var speed float64
	for _, t := range slskd.LatestTransfersPerFile(transfers) {
		if !slskd.IsTerminal(t) {
			speed += t.AverageSpeed
		}
	}
	return int64(speed)
}

func (s *Server) handleTransfers(w http.ResponseWriter, r *http.Request) {
	downloads, err := s.client.GetDownloads(r.Context())
	data := transfersData{}
	if err != nil {
		// show the page with an error banner rather than a blank screen
		data.Dirs = []dirRow{{FailState: "error: " + err.Error()}}
		s.render(w, "transfers.html", data)
		return
	}
	data.Dirs = transferDirs(s.ownedDownloads(downloads))
	s.render(w, "transfers.html", data)
}

// handleTransfersAPI serves the same data as JSON for live polling.
func (s *Server) handleTransfersAPI(w http.ResponseWriter, r *http.Request) {
	downloads, err := s.client.GetDownloads(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(transferDirs(s.ownedDownloads(downloads)))
}


// handleTransferRetry re-enqueues a failed directory from the same peer.
// POST: username, dir (remote album dir), artist, album
func (s *Server) handleTransferRetry(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	dir := r.FormValue("dir")
	artist := r.FormValue("artist")
	album := r.FormValue("album")
	if username == "" || dir == "" {
		http.Redirect(w, r, "/transfers", http.StatusSeeOther)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	// Clear the failed transfers for this directory first so the in-flight
	// dedup guard doesn't block the re-download.
	s.clearDirTransfers(ctx, username, dir)

	files, err := s.findFilesFor(ctx, username, dir, artist, album)
	if err != nil || len(files) == 0 {
		s.render(w, "error.html", map[string]string{
			"Error": "Retry failed — couldn't re-find the files (the peer's share may have changed): " + fmt.Sprint(err),
		})
		return
	}
	reqs := make([]slskd.EnqueueRequest, len(files))
	for i, f := range files {
		reqs[i] = slskd.EnqueueRequest{Filename: f.Filename, Size: f.Size}
	}
	if _, err := s.client.Enqueue(ctx, username, reqs); err != nil {
		s.render(w, "error.html", map[string]string{"Error": err.Error()})
		return
	}
	// Mark before redirecting: the watcher consults this to separate dig's
	// transfers from anything else sharing the slskd account.
	if err := s.cache.MarkOwn(username, slskd.AlbumDirFromRemote(dir)); err != nil {
		log.Printf("mark own %s/%s: %v", username, dir, err)
	}
	http.Redirect(w, r, "/transfers", http.StatusSeeOther)
}

// handleTransferClear removes failed transfer records for one directory.
func (s *Server) handleTransferClear(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	dir := r.FormValue("dir")
	if username == "" || dir == "" {
		http.Redirect(w, r, "/transfers", http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	s.clearDirTransfers(ctx, username, dir)
	http.Redirect(w, r, "/transfers", http.StatusSeeOther)
}

// clearDirTransfers cancels (with remove) transfers in the given
// user+directory. When the directory has failures, only failed records are
// removed (succeeded files belong to wrtag's handoff history). When it's
// fully completed, all records are removed — that's the "Clear" button on a
// completed card.
func (s *Server) clearDirTransfers(ctx context.Context, username, dir string) {
	downloads, err := s.client.GetDownloads(ctx)
	if err != nil {
		return
	}
	var dirTransfers []slskd.Transfer
	for _, t := range downloads[username] {
		if slskd.AlbumDir(t.Filename) == dir {
			dirTransfers = append(dirTransfers, t)
		}
	}
	hasFailures := false
	for _, t := range dirTransfers {
		if slskd.IsTerminal(t) && !slskd.IsSucceeded(t) {
			hasFailures = true
			break
		}
	}
	for _, t := range dirTransfers {
		if hasFailures && slskd.IsSucceeded(t) {
			continue
		}
		_ = s.client.CancelTransfer(ctx, username, t.ID, true)
	}
}

func shortFilename(fn string) string {
	if i := strings.LastIndexAny(fn, "\\/"); i >= 0 {
		return fn[i+1:]
	}
	return fn
}

// ---- rendering ----

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func queryEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "&", "%26"), "+", "%2B")
}

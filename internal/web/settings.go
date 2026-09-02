package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Ebonhawk3829/dig/internal/config"
	"github.com/Ebonhawk3829/dig/internal/notify"
)

// settingsView is the editable settings model. Fields map to config keys but
// are persisted as UI overrides in SQLite; env/YAML remain the base layer.
// Secrets are write-only: the API key and password fields render blank and
// are only updated when non-empty values are submitted.
type settingsView struct {
	// slskd
	SlskdURL    string
	SlskdAPIKey string // write-only
	// metadata
	MBBaseURL   string
	MBUserAgent string
	// library check (Layer 2 dedup)
	LibraryURL  string
	LibraryUser string
	LibraryPass string // write-only
	// notifications
	NotifyURL string
	// behaviour
	SearchTimeoutMS int
	// status flags (read-only, for the page)
	HasAPIKey    bool
	HasLibPass   bool
	LibraryOn    bool
	NotifyOn     bool
	Saved        bool
	TestResults  map[string]string
}

// loadOverrides applies any persisted UI overrides on top of the file/env config.
func (s *Server) loadOverrides() settingsView {
	v := settingsView{
		SlskdURL:        s.cfg.Slskd.URL,
		MBBaseURL:       s.cfg.MusicBrainz.BaseURL,
		MBUserAgent:     s.cfg.MusicBrainz.UserAgent,
		LibraryURL:      s.cfg.Library.SubsonicURL,
		LibraryUser:     s.cfg.Library.SubsonicUser,
		NotifyURL:       s.cfg.Notify.URL,
		SearchTimeoutMS: s.cfg.Slskd.SearchTimeoutMS,
		HasAPIKey:       s.cfg.Slskd.APIKey != "",
		HasLibPass:      s.cfg.Library.SubsonicPassword != "",
		LibraryOn:       s.library != nil,
		NotifyOn:        s.notify != nil,
	}
	if s.cache == nil {
		return v
	}
	var m map[string]string
	if ok, _ := s.cache.GetSetting("overrides", &m); ok {
		get := func(k, cur string) string {
			if val, ok := m[k]; ok && val != "" {
				return val
			}
			return cur
		}
		v.SlskdURL = get("slskd_url", v.SlskdURL)
		v.MBBaseURL = get("mb_base_url", v.MBBaseURL)
		v.MBUserAgent = get("mb_user_agent", v.MBUserAgent)
		v.LibraryURL = get("library_url", v.LibraryURL)
		v.LibraryUser = get("library_user", v.LibraryUser)
		v.NotifyURL = get("notify_url", v.NotifyURL)
		if val, ok := m["search_timeout_ms"]; ok && val != "" {
			if n := atoiSafe(val); n > 0 {
				v.SearchTimeoutMS = n
			}
		}
		if val, ok := m["slskd_api_key"]; ok && val != "" {
			v.HasAPIKey = true
		}
		if val, ok := m["library_pass"]; ok && val != "" {
			v.HasLibPass = true
		}
	}
	return v
}

// effectiveConfig merges file/env config with persisted UI overrides into a
// live config.Config. Called on settings save; the returned config is what
// the running server uses from then on.
func (s *Server) effectiveConfig() config.Config {
	cfg := s.cfg
	if s.cache == nil {
		return cfg
	}
	var m map[string]string
	if ok, _ := s.cache.GetSetting("overrides", &m); !ok {
		return cfg
	}
	get := func(k, cur string) string {
		if val, ok := m[k]; ok && val != "" {
			return val
		}
		return cur
	}
	cfg.Slskd.URL = get("slskd_url", cfg.Slskd.URL)
	cfg.Slskd.APIKey = get("slskd_api_key", cfg.Slskd.APIKey)
	cfg.MusicBrainz.BaseURL = get("mb_base_url", cfg.MusicBrainz.BaseURL)
	cfg.MusicBrainz.UserAgent = get("mb_user_agent", cfg.MusicBrainz.UserAgent)
	cfg.Library.SubsonicURL = get("library_url", cfg.Library.SubsonicURL)
	cfg.Library.SubsonicUser = get("library_user", cfg.Library.SubsonicUser)
	cfg.Library.SubsonicPassword = get("library_pass", cfg.Library.SubsonicPassword)
	cfg.Notify.URL = get("notify_url", cfg.Notify.URL)
	if val, ok := m["search_timeout_ms"]; ok && val != "" {
		if n := atoiSafe(val); n > 0 {
			cfg.Slskd.SearchTimeoutMS = n
		}
	}
	return cfg
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
		if n > 1_000_000 {
			return 0
		}
	}
	return n
}

// rebuildClients re-creates the live clients from the effective config.
func (s *Server) rebuildClients() {
	cfg := s.effectiveConfig()
	s.cfg = cfg
	s.client = newSlskdClient(cfg)
	s.guard = newGuard(s.client)
	s.mb = newMBClient(cfg, s.cache)
	s.library = newLibraryClient(cfg)
	s.notify = newNotifySender(cfg)
}

// ---- handlers ----

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	v := s.loadOverrides()
	s.render(w, "settings.html", v)
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	if s.cache == nil {
		s.render(w, "error.html", map[string]string{
			"Error": "Settings require the database (DIG_DB_PATH). Start dig with a database to use the settings UI.",
		})
		return
	}

	m := map[string]string{}
	// keep existing secret overrides if the form field is blank
	var existing map[string]string
	if ok, _ := s.cache.GetSetting("overrides", &existing); ok {
		for k, val := range existing {
			m[k] = val
		}
	}

	set := func(key, formKey string) {
		val := strings.TrimSpace(r.FormValue(formKey))
		if val != "" {
			m[key] = val
		}
	}
	set("slskd_url", "slskd_url")
	set("slskd_api_key", "slskd_api_key")
	set("mb_base_url", "mb_base_url")
	set("mb_user_agent", "mb_user_agent")
	set("library_url", "library_url")
	set("library_user", "library_user")
	set("library_pass", "library_pass")
	set("notify_url", "notify_url")
	if n := atoiSafe(r.FormValue("search_timeout_ms")); n > 0 {
		m["search_timeout_ms"] = itoa(n)
	}

	if err := s.cache.SetSetting("overrides", m); err != nil {
		s.render(w, "error.html", map[string]string{"Error": err.Error()})
		return
	}

	s.rebuildClients()

	v := s.loadOverrides()
	v.Saved = true
	s.render(w, "settings.html", v)
}

// handleSettingsTest probes one dependency and returns a short status line.
// GET /settings/test?which=slskd|musicbrainz|library|notify
func (s *Server) handleSettingsTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	// test against the values currently in the form (POST body) if provided,
	// else the live clients
	which := r.URL.Query().Get("which")
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		s.testRebuild(r)
	}
	defer s.rebuildClients() // restore live clients from persisted config

	var status string
	switch which {
	case "slskd":
		if _, err := s.client.GetDownloads(ctx); err != nil {
			status = "fail: " + shortErr(err)
		} else {
			status = "ok"
		}
	case "musicbrainz":
		if s.mb == nil {
			status = "not configured"
		} else if err := s.mb.Ping(ctx); err != nil {
			status = "fail: " + shortErr(err)
		} else {
			status = "ok"
		}
	case "library":
		if s.library == nil {
			status = "not configured"
		} else if err := s.library.Ping(ctx); err != nil {
			status = "fail: " + shortErr(err)
		} else {
			status = "ok"
		}
	case "notify":
		if s.notify == nil {
			status = "not configured"
		} else {
			s.notify.Send(notify.Event{State: "test", Detail: "sent from dig settings"})
			status = "sent — check your notification endpoint"
		}
	default:
		http.Error(w, `{"error":"unknown which"}`, http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"which": which, "status": status})
}

// testRebuild temporarily rebuilds clients from submitted form values so the
// operator can test before saving.
func (s *Server) testRebuild(r *http.Request) {
	cfg := s.cfg
	if v := strings.TrimSpace(r.FormValue("slskd_url")); v != "" {
		cfg.Slskd.URL = v
	}
	if v := strings.TrimSpace(r.FormValue("slskd_api_key")); v != "" {
		cfg.Slskd.APIKey = v
	}
	if v := strings.TrimSpace(r.FormValue("mb_base_url")); v != "" {
		cfg.MusicBrainz.BaseURL = v
	}
	if v := strings.TrimSpace(r.FormValue("mb_user_agent")); v != "" {
		cfg.MusicBrainz.UserAgent = v
	}
	if v := strings.TrimSpace(r.FormValue("library_url")); v != "" {
		cfg.Library.SubsonicURL = v
	}
	if v := strings.TrimSpace(r.FormValue("library_user")); v != "" {
		cfg.Library.SubsonicUser = v
	}
	if v := strings.TrimSpace(r.FormValue("library_pass")); v != "" {
		cfg.Library.SubsonicPassword = v
	}
	if v := strings.TrimSpace(r.FormValue("notify_url")); v != "" {
		cfg.Notify.URL = v
	}
	s.client = newSlskdClient(cfg)
	s.guard = newGuard(s.client)
	s.mb = newMBClient(cfg, s.cache)
	s.library = newLibraryClient(cfg)
	s.notify = newNotifySender(cfg)
}

func shortErr(err error) string {
	msg := err.Error()
	if len(msg) > 120 {
		msg = msg[:120] + "…"
	}
	return msg
}

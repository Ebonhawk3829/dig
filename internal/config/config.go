// Package config loads dig's YAML configuration with environment-variable
// overrides (DIG_ prefix, uppercase, underscores for nesting).
package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config is dig's full configuration.
type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Slskd       SlskdConfig       `yaml:"slskd"`
	MusicBrainz MusicBrainzConfig `yaml:"musicbrainz"`
	Scoring     ScoringConfig     `yaml:"scoring"`
	Library     LibraryConfig     `yaml:"library"`
	Notify      NotifyConfig      `yaml:"notify"`
	DB          DBConfig          `yaml:"db"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Auth AuthConfig `yaml:"auth"`
}

type AuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type SlskdConfig struct {
	URL             string `yaml:"url"`
	APIKey          string `yaml:"api_key"`
	SearchTimeoutMS int    `yaml:"search_timeout_ms"`
}

type MusicBrainzConfig struct {
	BaseURL           string  `yaml:"base_url"`
	UserAgent         string  `yaml:"user_agent"`
	RateLimitPerSec   float64 `yaml:"rate_limit_per_second"`
	CacheTTLSearch    int     `yaml:"cache_ttl_search"`
	CacheTTLDetail    int     `yaml:"cache_ttl_detail"`
}

type ScoringConfig struct {
	WeightCompleteness float64 `yaml:"weight_completeness"`
	WeightDirname      float64 `yaml:"weight_dirname"`
	WeightFormat       float64 `yaml:"weight_format"`
	WeightSource       float64 `yaml:"weight_source"`
	AcceptThreshold    float64 `yaml:"accept_threshold"`
	RejectThreshold    float64 `yaml:"reject_threshold"`
}

type LibraryConfig struct {
	SubsonicURL      string `yaml:"subsonic_url"`
	SubsonicUser     string `yaml:"subsonic_user"`
	SubsonicPassword string `yaml:"subsonic_password"`
}

type NotifyConfig struct {
	URL string `yaml:"url"`
}

type DBConfig struct {
	Path string `yaml:"path"`
}

// Defaults returns the built-in configuration used when a key is absent.
func Defaults() Config {
	return Config{
		Server: ServerConfig{Port: 8080},
		Slskd: SlskdConfig{
			URL:             "http://slskd:5030",
			SearchTimeoutMS: 15000,
		},
		MusicBrainz: MusicBrainzConfig{
			BaseURL:         "https://musicbrainz.org/ws/2",
			UserAgent:       "dig/0.1 (dig@example.com)",
			RateLimitPerSec: 1.0,
			CacheTTLSearch:  90,
			CacheTTLDetail:  21600,
		},
		Scoring: ScoringConfig{
			WeightCompleteness: 0.45,
			WeightDirname:      0.25,
			WeightFormat:       0.20,
			WeightSource:       0.10,
			AcceptThreshold:    0.70,
			RejectThreshold:    0.50,
		},
		DB: DBConfig{Path: "/data/dig.db"},
	}
}

// Load reads the YAML file at path (if it exists), applies defaults, then
// applies DIG_-prefixed environment overrides.
func Load(path string) (Config, error) {
	cfg := Defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return cfg, fmt.Errorf("parse %s: %w", path, err)
			}
		} else if !os.IsNotExist(err) {
			return cfg, fmt.Errorf("read %s: %w", path, err)
		}
	}

	applyEnv(&cfg)
	return cfg, nil
}

// applyEnv overrides config keys with DIG_-prefixed environment variables.
// Nesting maps to underscores: DIG_SLSKD_URL -> slskd.url.
func applyEnv(cfg *Config) {
	envStr := func(key string, dst *string) {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			*dst = v
		}
	}
	envInt := func(key string, dst *int) {
		if v, ok := os.LookupEnv(key); ok {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			}
		}
	}

	envStr("DIG_SERVER_AUTH_USERNAME", &cfg.Server.Auth.Username)
	envStr("DIG_SERVER_AUTH_PASSWORD", &cfg.Server.Auth.Password)
	envStr("DIG_SLSKD_URL", &cfg.Slskd.URL)
	envStr("DIG_SLSKD_API_KEY", &cfg.Slskd.APIKey)
	envInt("DIG_SLSKD_SEARCH_TIMEOUT_MS", &cfg.Slskd.SearchTimeoutMS)
	envStr("DIG_MUSICBRAINZ_BASE_URL", &cfg.MusicBrainz.BaseURL)
	envStr("DIG_MUSICBRAINZ_USER_AGENT", &cfg.MusicBrainz.UserAgent)
	envStr("DIG_LIBRARY_SUBSONIC_URL", &cfg.Library.SubsonicURL)
	envStr("DIG_LIBRARY_SUBSONIC_USER", &cfg.Library.SubsonicUser)
	envStr("DIG_LIBRARY_SUBSONIC_PASSWORD", &cfg.Library.SubsonicPassword)
	envStr("DIG_NOTIFY_URL", &cfg.Notify.URL)
	envStr("DIG_DB_PATH", &cfg.DB.Path)
}

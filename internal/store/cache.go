// Package store provides dig's minimal persistence: a TTL cache for
// metadata API responses. dig keeps no user data (see proposal §2/§7).
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Cache is a SQLite-backed key/value cache with per-entry TTLs.
type Cache struct {
	db *sql.DB
}

// Open opens (creating if needed) the cache database at path.
func Open(path string) (*Cache, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// modernc/sqlite is not safe for concurrent writes by default; serialize.
	db.SetMaxOpenConns(1)

	schema := `
CREATE TABLE IF NOT EXISTS mb_cache (
    key     TEXT PRIMARY KEY,
    value   BLOB NOT NULL,
    expires INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mb_cache_expires ON mb_cache(expires);`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	if err := ensureSettings(&Cache{db: db}); err != nil {
		db.Close()
		return nil, fmt.Errorf("settings schema: %w", err)
	}
	return &Cache{db: db}, nil
}

// Close closes the database.
func (c *Cache) Close() error { return c.db.Close() }

// Get returns the cached JSON value for key, or nil if absent/expired.
func (c *Cache) Get(key string, dst any) (bool, error) {
	var raw []byte
	var expires int64
	err := c.db.QueryRow(`SELECT value, expires FROM mb_cache WHERE key = ?`, key).
		Scan(&raw, &expires)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if time.Now().Unix() >= expires {
		_, _ = c.db.Exec(`DELETE FROM mb_cache WHERE key = ?`, key)
		return false, nil
	}
	if dst == nil {
		return true, nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return false, fmt.Errorf("cache unmarshal %s: %w", key, err)
	}
	return true, nil
}

// Set stores a JSON-serialisable value under key with the given TTL.
func (c *Cache) Set(key string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("cache marshal %s: %w", key, err)
	}
	_, err = c.db.Exec(`
INSERT INTO mb_cache (key, value, expires) VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, expires = excluded.expires`,
		key, raw, time.Now().Add(ttl).Unix())
	return err
}

// PurgeExpired removes expired entries. Called periodically.
func (c *Cache) PurgeExpired() (int64, error) {
	res, err := c.db.Exec(`DELETE FROM mb_cache WHERE expires < ?`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

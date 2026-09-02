package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// Settings is a key/value store for UI-editable configuration overrides
// (proposal §9: env/YAML remain the base layer; settings made in the UI are
// persisted here and take precedence). Values are JSON-encoded.
const settingsTable = `
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);`

// ensureSettings creates the settings table (called from Open).
func ensureSettings(c *Cache) error {
	_, err := c.db.Exec(settingsTable)
	return err
}

// GetSetting returns the stored JSON value for key into dst.
func (c *Cache) GetSetting(key string, dst any) (bool, error) {
	var raw []byte
	err := c.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return false, err
	}
	return true, nil
}

// SetSetting stores a JSON-serialisable value under key.
func (c *Cache) SetSetting(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = c.db.Exec(`
INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, raw, time.Now().Unix())
	return err
}

// DeleteSetting removes a key (reverts to the env/YAML default).
func (c *Cache) DeleteSetting(key string) error {
	_, err := c.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

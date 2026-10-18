// Owned transfer tracking: which slskd queue entries are dig's own.
//
// The downloads queue is shared by everything on the slskd instance — other
// tools enqueue into the same account. Notifications must only fire for
// transfers dig started, so each enqueue marks its (peer, directory) here and
// the watcher treats unmarked directories as someone else's business.
package store

import (
	"database/sql"
	"fmt"
)

const ownedSchema = `
CREATE TABLE IF NOT EXISTS owned_transfer (
    username TEXT NOT NULL,
    dir      TEXT NOT NULL,
    PRIMARY KEY (username, dir)
);`

// MarkOwn records (username, dir) as a directory dig enqueued. Idempotent:
// retries and re-downloads re-mark rather than duplicate.
//
// The mark precedes any notification decision, so the table is created here
// rather than only at open time: a query must never fall over for a table
// the schema has not applied yet.
func (c *Cache) MarkOwn(username, dir string) error {
	if _, err := c.db.Exec(ownedSchema); err != nil {
		return fmt.Errorf("owned schema: %w", err)
	}
	_, err := c.db.Exec(
		`INSERT OR IGNORE INTO owned_transfer (username, dir) VALUES (?,?)`,
		username, dir)
	return err
}

// IsOwn reports whether (username, dir) was enqueued by dig.
func (c *Cache) IsOwn(username, dir string) (bool, error) {
	row := c.db.QueryRow(
		`SELECT 1 FROM owned_transfer WHERE username = ? AND dir = ?`,
		username, dir)
	var one int
	err := row.Scan(&one)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ForgetOwn removes the mark after the transfer leaves the queue (cleared,
// finished and hidden); it keeps the table from growing without bound.
func (c *Cache) ForgetOwn(username, dir string) error {
	_, err := c.db.Exec(
		`DELETE FROM owned_transfer WHERE username = ? AND dir = ?`,
		username, dir)
	return err
}

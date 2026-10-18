package store

import (
	"path/filepath"
	"testing"
)

func TestOwnedTransferLifecycle(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "dig.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c.Close()

	// An unmarked directory is never treated as dig's own: that is the
	// whole point — rhapsode or anything else on the slskd instance must
	// not trigger notifications.
	own, err := c.IsOwn("peer", "Music\\Toto\\Toto IV")
	if err != nil {
		t.Fatalf("is own: %v", err)
	}
	if own {
		t.Fatal("unmarked directory reported as own")
	}

	// Mark at enqueue time; re-marks (retries, re-downloads) stay idempotent.
	if err := c.MarkOwn("peer", "Music\\Toto\\Toto IV"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := c.MarkOwn("peer", "Music\\Toto\\Toto IV"); err != nil {
		t.Fatalf("re-mark: %v", err)
	}
	own, err = c.IsOwn("peer", "Music\\Toto\\Toto IV")
	if err != nil || !own {
		t.Fatalf("marked directory not own: own=%v err=%v", own, err)
	}

	// The mark is per (username, dir): a different peer sharing the same
	// directory is not covered by this one's mark.
	other, err := c.IsOwn(" somebodyelse", "Music\\Toto\\Toto IV")
	if err != nil || other {
		t.Errorf("other peer's copy of the same dir reported own: %v", other)
	}

	// Forgetting on queue exit keeps the table bounded.
	if err := c.ForgetOwn("peer", "Music\\Toto\\Toto IV"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	own, err = c.IsOwn("peer", "Music\\Toto\\Toto IV")
	if err != nil || own {
		t.Fatalf("forgotten mark still own: own=%v err=%v", own, err)
	}
}

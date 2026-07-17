package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSnapshotTo verifies VACUUM INTO runs through the modernc driver and
// produces a consistent, openable copy with matching row counts.
func TestSnapshotTo(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	st, err := Open(src)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	if err := st.SetState("k1", map[string]any{"a": 1}); err != nil {
		t.Fatalf("setstate: %v", err)
	}
	if err := st.LogTrade(map[string]any{"side": "buy", "code": "X", "shares": 3.0, "price": 2.0, "status": "filled"}); err != nil {
		t.Fatalf("logtrade: %v", err)
	}

	dst := filepath.Join(dir, "snap.db")
	if err := st.SnapshotTo(dst); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("snapshot file missing: %v", err)
	}

	// Snapshot must be openable and carry the same rows.
	st2, err := Open(dst)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer st2.Close()
	if got := st2.GetStateMap("k1"); got["a"] == nil {
		t.Fatalf("snapshot missing runtime_state row: %#v", got)
	}
	if trades := st2.RecentTrades(10); len(trades) != 1 {
		t.Fatalf("snapshot trade count = %d, want 1", len(trades))
	}

	// Overwriting a pre-existing (not-open) target must succeed: SnapshotTo
	// removes the stale file before VACUUM INTO.
	stale := filepath.Join(dir, "stale.db")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	if err := st.SnapshotTo(stale); err != nil {
		t.Fatalf("snapshot over existing file: %v", err)
	}
}

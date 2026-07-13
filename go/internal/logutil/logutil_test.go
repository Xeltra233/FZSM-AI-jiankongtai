package logutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupAgeAndBackups(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "fzsm_logutil_test")
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	active := filepath.Join(dir, "bot.log")
	if err := os.WriteFile(active, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	old := filepath.Join(dir, "bot.log.oldx")
	if err := os.WriteFile(old, []byte("o"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-10 * 24 * time.Hour)
	_ = os.Chtimes(old, past, past)

	for i := 0; i < 12; i++ {
		p := filepath.Join(dir, fmt.Sprintf("bot.log.20260101-%02d0000", i))
		if err := os.WriteFile(p, []byte("b"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := Cleanup(dir, "bot.log", 7, 7); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatal("active missing")
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("old should be deleted")
	}
	if len(entries) > 8 {
		t.Fatalf("too many remaining: %d", len(entries))
	}
}

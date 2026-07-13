package trader

import (
	"testing"
	"time"
)

func TestParseBankruptcyUntilHourMin(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.Local)
	until, msg := parseBankruptcyUntil("\u7834\u4ea7\u51b7\u5374\u4e2d\uff0c\u8fd8\u6709 20 \u5c0f\u65f6 41 \u5206\u949f\u624d\u80fd\u4ea4\u6613", now)
	if until.IsZero() {
		t.Fatal("until zero")
	}
	// 20h41m + 30s buffer
	want := now.Add(20*time.Hour + 41*time.Minute + 30*time.Second)
	if until.Unix() != want.Unix() {
		t.Fatalf("until=%v want=%v msg=%s", until, want, msg)
	}
	if msg == "" {
		t.Fatal("empty msg")
	}
}

func TestParseBankruptcyUntilMinutes(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.Local)
	until, _ := parseBankruptcyUntil("\u7834\u4ea7\u51b7\u5374\u4e2d\uff0c\u8fd8\u6709 15 \u5206\u949f\u624d\u80fd\u4ea4\u6613", now)
	want := now.Add(15*time.Minute + 30*time.Second)
	if until.Unix() != want.Unix() {
		t.Fatalf("until=%v want=%v", until, want)
	}
}

func TestParseBankruptcyNoMatch(t *testing.T) {
	until, msg := parseBankruptcyUntil("\u4f59\u989d\u4e0d\u8db3", time.Now())
	if !until.IsZero() || msg != "" {
		t.Fatalf("unexpected until=%v msg=%q", until, msg)
	}
}

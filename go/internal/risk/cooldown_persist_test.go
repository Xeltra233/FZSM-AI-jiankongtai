package risk

import "testing"

func TestCooldownPersistAcrossManager(t *testing.T) {
	m1 := New(map[string]any{"cooldown_sec": 90.0, "reduce_cooldown_sec": 300.0})
	m1.MarkReduce(62)
	exported := m1.ExportCooldowns()
	if len(exported) != 1 {
		t.Fatalf("export=%v", exported)
	}
	m2 := New(map[string]any{"cooldown_sec": 90.0, "reduce_cooldown_sec": 300.0})
	m2.LoadCooldowns(exported)
	if !m2.InCooldown(62) {
		t.Fatal("cooldown lost after LoadCooldowns")
	}
	if m2.InCooldown(1) {
		t.Fatal("unexpected cooldown")
	}
}

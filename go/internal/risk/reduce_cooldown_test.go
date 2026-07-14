package risk

import "testing"

func TestMarkReduceLongerThanTrade(t *testing.T) {
	m := New(map[string]any{
		"cooldown_sec":        90.0,
		"reduce_cooldown_sec": 300.0,
	})
	m.MarkTrade(1)
	tradeUntil := m.Cooldowns[1]
	m.MarkReduce(2)
	reduceUntil := m.Cooldowns[2]
	if reduceUntil-tradeUntil < 200 {
		t.Fatalf("reduce cooldown not longer enough: trade=%v reduce=%v", tradeUntil, reduceUntil)
	}
	if !m.InCooldown(2) {
		t.Fatal("expected reduce cooldown active")
	}
}

package flags

import (
	"path/filepath"
	"testing"

	"fzsmbot/internal/config"
	"fzsmbot/internal/storage"
)

func TestDerivativesFlagDefaultsOffAndPersistsOverride(t *testing.T) {
	cfg := &config.Config{Derivatives: map[string]any{"trade_enabled": false}}
	st, err := storage.Open(filepath.Join(t.TempDir(), "flags.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	before := Get(cfg, st)["values"].(map[string]any)
	if asBool(before["derivatives.trade_enabled"], true) {
		t.Fatal("derivatives live trading must default off")
	}
	if _, err := Set(cfg, st, "derivatives.trade_enabled", true); err != nil {
		t.Fatal(err)
	}
	after := Get(cfg, st)["values"].(map[string]any)
	if !asBool(after["derivatives.trade_enabled"], false) {
		t.Fatal("explicit derivatives enable was not persisted")
	}
}

func TestPaidPremiumFlagDefaultsOffAndPersistsOverride(t *testing.T) {
	cfg := &config.Config{Lottery: map[string]any{"auto_draw_premium_paid": false}}
	st, err := storage.Open(filepath.Join(t.TempDir(), "paid-premium-flags.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	before := Get(cfg, st)["values"].(map[string]any)
	if asBool(before["lottery.auto_draw_premium_paid"], true) {
		t.Fatal("paid premium must default off")
	}
	if _, err := Set(cfg, st, "lottery.auto_draw_premium_paid", true); err != nil {
		t.Fatal(err)
	}
	after := Get(cfg, st)["values"].(map[string]any)
	if !asBool(after["lottery.auto_draw_premium_paid"], false) {
		t.Fatal("paid premium override not persisted")
	}
}

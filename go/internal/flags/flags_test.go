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

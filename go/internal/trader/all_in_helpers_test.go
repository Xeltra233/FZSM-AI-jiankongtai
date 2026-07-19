package trader

import (
	"errors"
	"testing"

	"fzsmbot/internal/risk"
)

func TestClampBuySharesFromPreview(t *testing.T) {
	for _, tc := range []struct {
		name    string
		preview map[string]any
		want    int
	}{
		{"remaining", map[string]any{"buy_limit_remaining": 37.0}, 37},
		{"order_limit", map[string]any{"order_limit_shares": 80}, 80},
		{"quoted", map[string]any{"shares": 25.0}, 25},
		{"zero_remaining", map[string]any{"buy_limit_remaining": 0.0}, 0},
		{"unchanged", map[string]any{}, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampBuyShares(100, tc.preview); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

func TestNextBuySharesUsesServerRemaining(t *testing.T) {
	if got := nextBuySharesAfterLimit(100, errors.New("买入数量超过上限：当前剩余 2 股，本次想买 100 股"), nil, 0.5); got != 2 {
		t.Fatalf("got %d want 2", got)
	}
	if got := nextBuySharesAfterLimit(100, errors.New("买入数量超过上限：当前剩余 0 股"), nil, 0.5); got != 0 {
		t.Fatalf("got %d want 0", got)
	}
	if got := nextBuySharesAfterLimit(100, errors.New("买入数量超过当前单笔上限"), nil, 0.5); got != 50 {
		t.Fatalf("got %d want 50", got)
	}
}

func TestRetryableBuyLimit(t *testing.T) {
	if !retryableBuyLimit(errors.New("buy status=422: 买入数量超过当前单笔上限"), nil) {
		t.Fatal("single-order limit should retry")
	}
	if !retryableBuyLimit(errors.New("status=422"), map[string]any{"raw": "买入数量超过上限：当前剩余 2 股"}) {
		t.Fatal("remaining limit should retry")
	}
	if retryableBuyLimit(errors.New("股票已涨停"), nil) {
		t.Fatal("limit-up should switch candidate, not shrink retry")
	}
}

func TestAllInRaisesNewEntryThroughput(t *testing.T) {
	rm := risk.New(map[string]any{"max_new_entries_per_cycle": 1, "all_in_max_new_entries_per_cycle": 3})
	td := &Trader{Risk: rm, Control: map[string]any{"capital_style": "all_in"}, Regime: map[string]any{"max_new_entries_per_cycle": 1}}
	rm.SetControl(td.Control)
	if got := td.maxNewEntries(); got != 3 {
		t.Fatalf("got %d want 3", got)
	}
}

func TestAllInDoesNotOverrideRegimeZeroEntries(t *testing.T) {
	rm := risk.New(map[string]any{"max_new_entries_per_cycle": 1, "all_in_max_new_entries_per_cycle": 3})
	td := &Trader{Risk: rm, Control: map[string]any{"capital_style": "all_in"}, Regime: map[string]any{"max_new_entries_per_cycle": 0}}
	rm.SetControl(td.Control)
	if got := td.maxNewEntries(); got != 0 {
		t.Fatalf("risk-off zero-entry gate overridden: %d", got)
	}
}

func TestFailedTradeDoesNotCountAsSuccess(t *testing.T) {
	if tradeSucceeded(map[string]any{"status": "error"}) {
		t.Fatal("error must not consume successful-entry budget")
	}
	if !tradeSucceeded(map[string]any{"status": "submitted"}) {
		t.Fatal("submitted trade should count")
	}
}

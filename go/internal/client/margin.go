package client

import "fmt"

// StocksFutures returns futures contract snapshot.
// Live probe: GET /futures -> 200.
func (c *Client) StocksFutures() ([]any, int, error) {
	return c.StocksList("/futures")
}

// StocksMarginPositions returns open margin/futures positions.
// Live probe: GET /margin/positions -> 200 (may be empty).
func (c *Client) StocksMarginPositions() ([]any, int, error) {
	return c.StocksList("/margin/positions")
}

// StocksMarginOpen opens a margin/futures position.
// Live probe: POST /margin/open exists (business 403/cooldown possible).
// Callers must assemble valid sizing fields; do not blind-fire.
func (c *Client) StocksMarginOpen(body map[string]any) (map[string]any, error) {
	if body == nil {
		body = map[string]any{}
	}
	code, data, err := c.StocksPost("/margin/open", body)
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("margin open status=%d data=%v", code, data)
	}
	if raw, ok := data.(map[string]any); ok {
		if ok2, has := raw["success"].(bool); has && !ok2 {
			return m, fmt.Errorf("margin open failed: %v", raw["message"])
		}
	}
	return m, nil
}

// StocksMarginClose closes a margin/futures position by valid position id.
// Live probe: POST /margin/close exists (400 without valid position id).
func (c *Client) StocksMarginClose(body map[string]any) (map[string]any, error) {
	if body == nil {
		body = map[string]any{}
	}
	code, data, err := c.StocksPost("/margin/close", body)
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("margin close status=%d data=%v", code, data)
	}
	if raw, ok := data.(map[string]any); ok {
		if ok2, has := raw["success"].(bool); has && !ok2 {
			return m, fmt.Errorf("margin close failed: %v", raw["message"])
		}
	}
	return m, nil
}

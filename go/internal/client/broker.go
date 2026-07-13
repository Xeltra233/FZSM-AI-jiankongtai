package client

import "fmt"

// StocksBrokerLike likes a broker candidate.
// Live probe: POST /broker/like {"candidate_id":20} -> 200 {"liked":true}
func (c *Client) StocksBrokerLike(candidateID any) (map[string]any, error) {
	if candidateID == nil || fmt.Sprint(candidateID) == "" || fmt.Sprint(candidateID) == "<nil>" {
		return nil, fmt.Errorf("broker like candidate_id required")
	}
	code, data, err := c.StocksPost("/broker/like", map[string]any{"candidate_id": candidateID})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("broker like status=%d data=%v", code, data)
	}
	if raw, ok := data.(map[string]any); ok {
		if ok2, has := raw["success"].(bool); has && !ok2 {
			return m, fmt.Errorf("broker like failed: %v", raw["message"])
		}
	}
	return m, nil
}

// StocksUnderwriterList returns underwriter candidates/orders list.
// Live probe: GET /broker/underwriter/list -> 200 (may be empty).
func (c *Client) StocksUnderwriterList() ([]any, int, error) {
	return c.StocksList("/broker/underwriter/list")
}

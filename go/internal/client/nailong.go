package client

import "fmt"

func (c *Client) LotteryNailong(multiplier int, count int) (map[string]any, error) {
	if multiplier <= 0 {
		multiplier = 1
	}
	if count <= 0 {
		count = 1
	}
	code, data, err := c.LotteryPost("/lottery/api/nailong", map[string]any{
		"multiplier": multiplier,
		"count":      count,
	})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("lottery nailong status=%d data=%v", code, data)
	}
	// success:false in body
	if ok, has := m["success"].(bool); has && !ok {
		return m, fmt.Errorf("lottery nailong failed: %v", m["message"])
	}
	return m, nil
}

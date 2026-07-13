package client

import "fmt"

func (c *Client) LotteryYolo() (map[string]any, error) {
	code, data, err := c.LotteryPost("/lottery/api/yolo", map[string]any{})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("lottery yolo status=%d data=%v", code, data)
	}
	// some APIs put success on outer envelope
	if raw, ok := data.(map[string]any); ok {
		if ok2, has := raw["success"].(bool); has && !ok2 {
			return m, fmt.Errorf("lottery yolo failed: %v", raw["message"])
		}
	}
	return m, nil
}

// LotteryBorrow creates a loan. source is offer/source id from loan offers list.
func (c *Client) LotteryBorrow(amount float64, source any) (map[string]any, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("lottery borrow amount must be > 0")
	}
	body := map[string]any{"amount": amount}
	if source != nil && fmt.Sprint(source) != "" && fmt.Sprint(source) != "<nil>" {
		body["source"] = source
	}
	code, data, err := c.LotteryPost("/lottery/api/loan", body)
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if code >= 400 {
		return m, fmt.Errorf("lottery borrow status=%d data=%v", code, data)
	}
	if raw, ok := data.(map[string]any); ok {
		if ok2, has := raw["success"].(bool); has && !ok2 {
			return m, fmt.Errorf("lottery borrow failed: %v", raw["message"])
		}
	}
	return m, nil
}

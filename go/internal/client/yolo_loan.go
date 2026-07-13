package client

import (
	"fmt"
	"strings"
)

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

func lotteryFail(m map[string]any, data any, code int, op string) error {
	if code >= 400 {
		msg := ""
		if raw, ok := data.(map[string]any); ok {
			msg = fmt.Sprint(raw["message"])
		}
		if msg != "" && msg != "<nil>" {
			return fmt.Errorf("%s status=%d message=%s", op, code, msg)
		}
		return fmt.Errorf("%s status=%d data=%v", op, code, data)
	}
	if raw, ok := data.(map[string]any); ok {
		if ok2, has := raw["success"].(bool); has && !ok2 {
			return fmt.Errorf("%s failed: %v", op, raw["message"])
		}
	}
	return nil
}

// LotteryVipRoom gets one room detail (includes round/hands when present).
func (c *Client) LotteryVipRoom(roomID any) (map[string]any, error) {
	path := fmt.Sprintf("/lottery/api/vip/rooms/%v", roomID)
	code, data, err := c.LotteryGet(path)
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if err := lotteryFail(m, data, code, "vip room"); err != nil {
		return m, err
	}
	return m, nil
}

// LotteryVipJoin joins a VIP room as player (default) or spectator.
func (c *Client) LotteryVipJoin(roomID any, asSpectator bool, password string) (map[string]any, error) {
	body := map[string]any{}
	if asSpectator {
		body["as_spectator"] = true
	}
	if strings.TrimSpace(password) != "" {
		body["password"] = password
	}
	path := fmt.Sprintf("/lottery/api/vip/rooms/%v/join", roomID)
	code, data, err := c.LotteryPost(path, body)
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if err := lotteryFail(m, data, code, "vip join"); err != nil {
		return m, err
	}
	return m, nil
}

func (c *Client) LotteryVipLeave(roomID any) (map[string]any, error) {
	path := fmt.Sprintf("/lottery/api/vip/rooms/%v/leave", roomID)
	code, data, err := c.LotteryPost(path, map[string]any{})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if err := lotteryFail(m, data, code, "vip leave"); err != nil {
		return m, err
	}
	return m, nil
}

func (c *Client) LotteryVipReady(roomID any, ready bool) (map[string]any, error) {
	path := fmt.Sprintf("/lottery/api/vip/rooms/%v/ready", roomID)
	code, data, err := c.LotteryPost(path, map[string]any{"ready": ready})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if err := lotteryFail(m, data, code, "vip ready"); err != nil {
		return m, err
	}
	return m, nil
}

func (c *Client) LotteryVipStart(roomID any, clientSeed string) (map[string]any, error) {
	body := map[string]any{}
	if strings.TrimSpace(clientSeed) != "" {
		body["client_seed"] = clientSeed
	}
	path := fmt.Sprintf("/lottery/api/vip/rooms/%v/start", roomID)
	code, data, err := c.LotteryPost(path, body)
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if err := lotteryFail(m, data, code, "vip start"); err != nil {
		return m, err
	}
	return m, nil
}

// LotteryVipBet places a bet on an active round. Multipliers commonly: 1/2/3/5.
func (c *Client) LotteryVipBet(roundID any, betMultiplier int) (map[string]any, error) {
	if betMultiplier <= 0 {
		betMultiplier = 1
	}
	path := fmt.Sprintf("/lottery/api/vip/rounds/%v/bet", roundID)
	code, data, err := c.LotteryPost(path, map[string]any{"bet_multiplier": betMultiplier})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if err := lotteryFail(m, data, code, "vip bet"); err != nil {
		return m, err
	}
	return m, nil
}

// LotteryDeposit creates a deposit order.
func (c *Client) LotteryDeposit(amount float64, durationHours int, rolloverMode string) (map[string]any, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("lottery deposit amount must be > 0")
	}
	if durationHours <= 0 {
		durationHours = 24
	}
	if strings.TrimSpace(rolloverMode) == "" {
		rolloverMode = "none"
	}
	body := map[string]any{
		"amount":         amount,
		"duration_hours": durationHours,
		"rollover_mode":  rolloverMode,
	}
	code, data, err := c.LotteryPost("/lottery/api/deposit", body)
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if err := lotteryFail(m, data, code, "lottery deposit"); err != nil {
		return m, err
	}
	return m, nil
}

func (c *Client) LotteryDepositWithdraw() (map[string]any, error) {
	code, data, err := c.LotteryPost("/lottery/api/deposit/withdraw", map[string]any{})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if err := lotteryFail(m, data, code, "lottery deposit withdraw"); err != nil {
		return m, err
	}
	return m, nil
}

func (c *Client) LotteryDepositRollover() (map[string]any, error) {
	code, data, err := c.LotteryPost("/lottery/api/deposit/rollover", map[string]any{})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if err := lotteryFail(m, data, code, "lottery deposit rollover"); err != nil {
		return m, err
	}
	return m, nil
}

func (c *Client) LotteryLoanRepay(loanID any) (map[string]any, error) {
	body := map[string]any{}
	if loanID != nil && fmt.Sprint(loanID) != "" && fmt.Sprint(loanID) != "<nil>" {
		body["loan_id"] = loanID
	}
	code, data, err := c.LotteryPost("/lottery/api/loan/repay", body)
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if err := lotteryFail(m, data, code, "lottery loan repay"); err != nil {
		return m, err
	}
	return m, nil
}

func (c *Client) LotteryLoanRepayAll() (map[string]any, error) {
	code, data, err := c.LotteryPost("/lottery/api/loan/repay_all", map[string]any{})
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if err := lotteryFail(m, data, code, "lottery loan repay_all"); err != nil {
		return m, err
	}
	return m, nil
}

func (c *Client) LotteryOfferCreate(amount float64, dailyRate float64, samedayPenalty float64, minBorrow float64, maxBorrow float64) (map[string]any, error) {
	body := map[string]any{
		"amount":          amount,
		"daily_rate":      dailyRate,
		"sameday_penalty": samedayPenalty,
		"min_borrow":      minBorrow,
		"max_borrow":      maxBorrow,
	}
	code, data, err := c.LotteryPost("/lottery/api/offers", body)
	if err != nil {
		return nil, err
	}
	m := unwrapData(data)
	m["_http_status"] = code
	if err := lotteryFail(m, data, code, "lottery offer create"); err != nil {
		return m, err
	}
	return m, nil
}

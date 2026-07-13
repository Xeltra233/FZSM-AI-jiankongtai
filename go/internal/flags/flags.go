package flags

import (
	"fmt"
	"time"

	"fzsmbot/internal/config"
	"fzsmbot/internal/storage"
)

type Spec struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	Key     string `json:"key"`
	Label   string `json:"label"`
	Group   string `json:"group"`
	Risk    string `json:"risk"`
	Desc    string `json:"desc"`
}

var Specs = []Spec{
	{"lottery.auto_checkin", "lottery", "auto_checkin", "自动签到", "lottery", "low", "每日免费签到，优先执行"},
	{"lottery.auto_draw_free", "lottery", "auto_draw_free", "自动免费抽", "lottery", "low", "有免费次数时自动抽"},
	{"lottery.auto_draw_premium_free", "lottery", "auto_draw_premium_free", "自动高级免费抽", "lottery", "low", "有高级免费次数时自动抽"},
	{"lottery.auto_slot", "lottery", "auto_slot", "老虎机", "lottery", "high", "正EV门槛：理论/样本正EV才自动转"},
	{"lottery.auto_yolo", "lottery", "auto_yolo", "搏一搏", "lottery", "high", "正EV门槛：默认负期望拦截，正EV才允许"},
	{"lottery.auto_nailong", "lottery", "auto_nailong", "奶龙机", "lottery", "high", "与老虎机同接口；正EV门槛"},
	{"lottery.auto_vip", "lottery", "auto_vip", "自动进VIP", "lottery", "high", "嵌套join路径已确认；正式入座受余额门槛"},
	{"lottery.auto_vip_bet", "lottery", "auto_vip_bet", "VIP自动下注", "lottery", "high", "正EV门槛；嵌套bet路径已确认，需回合且过门槛"},
	{"lottery.auto_vip_observe", "lottery", "auto_vip_observe", "VIP观战收样", "lottery", "low", "观战/只读观察房间与历史，自动收集样本，不下注"},
	{"lottery.auto_borrow_zero_rate", "lottery", "auto_borrow_zero_rate", "自动借零息", "lottery", "mid", "仅零息且edge通过；默认扫描不借"},
	{"lottery.auto_deposit", "lottery", "auto_deposit", "自动存款", "lottery", "mid", "需配置金额，对比机会成本"},
	{"lottery.auto_bankruptcy", "lottery", "auto_bankruptcy", "自动破产保护", "lottery", "high", "仅 EV 门控场景，默认关"},
	{"derivatives.trade_enabled", "derivatives", "trade_enabled", "期货实盘下单", "derivatives", "high", "正EV门槛：需净边为正，关闭时仅分析"},
	{"brokers.auto_like", "brokers", "auto_like", "券商自动点赞", "brokers", "low", "点赞热门券商"},
	{"brokers.auto_underwrite", "brokers", "auto_underwrite", "自动承销", "brokers", "high", "正EV门槛，默认关"},
	{"risk.edge_gate_enabled", "risk", "edge_gate_enabled", "正EV门槛门控", "risk", "mid", "开启后高风险自动执行必须通过正EV门槛；关闭则只看模块开关"},
	{"risk.edge_history_enabled", "risk", "edge_history_enabled", "高风险样本历史", "risk", "low", "开启后记录老虎机/搏一搏/借贷等最近样本，供门槛分析"},
}

func sectionMap(cfg *config.Config, section string) map[string]any {
	switch section {
	case "lottery":
		if cfg.Lottery == nil {
			return map[string]any{}
		}
		return cfg.Lottery
	case "derivatives":
		if cfg.Derivatives == nil {
			return map[string]any{}
		}
		return cfg.Derivatives
	case "brokers":
		if cfg.Brokers == nil {
			return map[string]any{}
		}
		return cfg.Brokers
	case "risk":
		if cfg.Risk == nil {
			return map[string]any{}
		}
		return cfg.Risk
	default:
		return map[string]any{}
	}
}

func asBool(v any, def bool) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch t {
		case "1", "true", "TRUE", "yes", "on", "开", "开启":
			return true
		case "0", "false", "FALSE", "no", "off", "关", "关闭":
			return false
		}
	case float64:
		return t != 0
	case int:
		return t != 0
	}
	return def
}

func Defaults(cfg *config.Config) map[string]bool {
	out := map[string]bool{}
	for _, sp := range Specs {
		sec := sectionMap(cfg, sp.Section)
		def := false
		switch sp.ID {
		case "risk.edge_gate_enabled", "risk.edge_history_enabled", "lottery.auto_vip_observe":
			def = true
		case "lottery.auto_checkin", "lottery.auto_draw_free", "lottery.auto_draw_premium_free", "brokers.auto_like":
			def = true
		}
		out[sp.ID] = asBool(sec[sp.Key], def)
	}
	return out
}

func Get(cfg *config.Config, st *storage.Storage) map[string]any {
	base := Defaults(cfg)
	saved := st.GetStateMap("feature_flags")
	values := map[string]bool{}
	for k, v := range base {
		values[k] = v
	}
	for k, v := range saved {
		if _, ok := values[k]; ok {
			values[k] = asBool(v, values[k])
		}
	}
	items := make([]map[string]any, 0, len(Specs))
	for _, sp := range Specs {
		val := values[sp.ID]
		def := base[sp.ID]
		_, inSaved := saved[sp.ID]
		items = append(items, map[string]any{
			"id": sp.ID, "section": sp.Section, "key": sp.Key, "label": sp.Label,
			"group": sp.Group, "risk": sp.Risk, "desc": sp.Desc,
			"value": val, "default": def, "overridden": val != def || inSaved,
		})
	}
	valsObj := map[string]any{}
	for k, v := range values {
		valsObj[k] = v
	}
	return map[string]any{
		"ts":     time.Now().Unix(),
		"values": valsObj,
		"items":  items,
		"groups": map[string]any{
			"lottery":     "抽奖/搏一搏/VIP",
			"derivatives": "期货",
			"brokers":     "券商",
			"risk":        "高风险门槛",
		},
	}
}

func Set(cfg *config.Config, st *storage.Storage, id string, value bool) (map[string]any, error) {
	known := map[string]bool{}
	for _, sp := range Specs {
		known[sp.ID] = true
	}
	if !known[id] {
		return nil, fmt.Errorf("unknown feature flag: %s", id)
	}
	saved := st.GetStateMap("feature_flags")
	saved[id] = value
	saved["_updated_at"] = float64(time.Now().UnixNano()) / 1e9
	saved["_updated_flag"] = id
	// keep only known + meta
	clean := map[string]any{}
	for k, v := range saved {
		if len(k) > 0 && (k[0] == '_' || known[k]) {
			clean[k] = v
		}
	}
	if err := st.SetState("feature_flags", clean); err != nil {
		return nil, err
	}
	return Get(cfg, st), nil
}

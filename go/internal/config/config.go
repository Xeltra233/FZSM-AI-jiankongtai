package config

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	APIBase    string `yaml:"api_base" json:"api_base"`
	CookieFile string `yaml:"cookie_file" json:"cookie_file"`
	Mode       string `yaml:"mode" json:"mode"`
	Storage    struct {
		DBPath string `yaml:"db_path" json:"db_path"`
		LogDir string `yaml:"log_dir" json:"log_dir"`
	} `yaml:"storage" json:"storage"`
	Dashboard struct {
		Host       string `yaml:"host" json:"host"`
		Port       int    `yaml:"port" json:"port"`
		RefreshSec int    `yaml:"refresh_sec" json:"refresh_sec"`
	} `yaml:"dashboard" json:"dashboard"`
	Auth struct {
		KeepaliveEnabled     bool `yaml:"keepalive_enabled" json:"keepalive_enabled"`
		KeepaliveEveryCycles int  `yaml:"keepalive_every_cycles" json:"keepalive_every_cycles"`
		ProbeLottery         bool `yaml:"probe_lottery" json:"probe_lottery"`
	} `yaml:"auth" json:"auth"`
	Loop        map[string]any `yaml:"loop" json:"loop"`
	Universe    map[string]any `yaml:"universe" json:"universe"`
	Farm        map[string]any `yaml:"farm" json:"farm"`
	Lottery     map[string]any `yaml:"lottery" json:"lottery"`
	Derivatives map[string]any `yaml:"derivatives" json:"derivatives"`
	Brokers     map[string]any `yaml:"brokers" json:"brokers"`
	Strategy    map[string]any `yaml:"strategy" json:"strategy"`
	Risk        map[string]any `yaml:"risk" json:"risk"`
	Regime      map[string]any `yaml:"regime" json:"regime"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	raw = normalize(raw)
	jb, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(jb, &c); err != nil {
		return nil, err
	}
	if c.APIBase == "" {
		c.APIBase = "https://fanzisima.xyz/stocks/api"
	}
	if c.Storage.DBPath == "" {
		c.Storage.DBPath = "data/bot.db"
	}
	if c.Storage.LogDir == "" {
		c.Storage.LogDir = "logs"
	}
	if c.CookieFile == "" {
		c.CookieFile = "auth/cookies.json"
	}
	if c.Dashboard.Host == "" {
		c.Dashboard.Host = "127.0.0.1"
	}
	if c.Dashboard.Port == 0 {
		c.Dashboard.Port = 8788
	}
	if c.Dashboard.RefreshSec == 0 {
		c.Dashboard.RefreshSec = 4
	}
	if c.Mode == "" {
		c.Mode = "live"
	}
	if c.Auth.KeepaliveEveryCycles <= 0 {
		c.Auth.KeepaliveEveryCycles = 3
	}
	return &c, nil
}

func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalize(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = normalize(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalize(val)
		}
		return out
	default:
		return v
	}
}

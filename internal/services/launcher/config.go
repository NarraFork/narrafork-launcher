package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// configPrefKey 是启动器配置在 preferences 表中的键（值为 JSON）。
const configPrefKey = "launcher.config"

// Config 是启动器的全部用户配置。
//
// Port / Host 为零值时不向 NarraFork 传参，沿用它自己的 ~/.narrafork/settings.json；
// 实际监听地址一律以服务输出的 `running on …` 为准。
type Config struct {
	Port        int    `json:"port"`
	Host        string `json:"host"`
	AutoStart   bool   `json:"autoStart"`
	AutoUpdate  bool   `json:"autoUpdate"`
	Channel     string `json:"channel"`     // "stable" | "beta"
	OpenMode    string `json:"openMode"`    // "inline" | "external" | "none"
	OpenOnReady bool   `json:"openOnReady"` // 服务就绪后自动打开
	CloseAction string `json:"closeAction"` // "minimize" | "quit"
	// BinaryPath 非空时启动该可执行文件，不使用下载目录里的版本。
	BinaryPath string `json:"binaryPath"`
}

func defaultConfig() Config {
	return Config{
		AutoStart:   true,
		AutoUpdate:  true,
		Channel:     "stable",
		OpenMode:    "inline",
		OpenOnReady: true,
		CloseAction: "minimize",
	}
}

// normalize 校验并修正配置；非法值回落默认，返回错误只针对用户明确输入的非法端口 / 主机。
func (c Config) normalize() (Config, error) {
	d := defaultConfig()
	if c.Channel != "stable" && c.Channel != "beta" {
		c.Channel = d.Channel
	}
	switch c.OpenMode {
	case "inline", "external", "none":
	default:
		c.OpenMode = d.OpenMode
	}
	if c.CloseAction != "minimize" && c.CloseAction != "quit" {
		c.CloseAction = d.CloseAction
	}
	if c.Port < 0 || c.Port > 65535 {
		return c, fmt.Errorf("invalid port %d", c.Port)
	}
	c.Host = strings.TrimSpace(c.Host)
	if c.Host != "" && !validHost(c.Host) {
		return c, fmt.Errorf("invalid host %q", c.Host)
	}
	c.BinaryPath = strings.TrimSpace(c.BinaryPath)
	return c, nil
}

// validHost 只接受 IP 字面量或主机名字符，防止把参数注入到命令行。
func validHost(h string) bool {
	if net.ParseIP(strings.Trim(h, "[]")) != nil {
		return true
	}
	if len(h) > 253 {
		return false
	}
	for _, r := range h {
		ok := r == '-' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

func (s *Service) loadConfig(ctx context.Context) Config {
	raw, err := s.prefs.Get(ctx, configPrefKey)
	cfg := defaultConfig()
	if err != nil || raw == "" {
		return cfg
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		s.log.Warn("launcher config corrupt; using defaults", "err", err)
		return defaultConfig()
	}
	cfg, _ = cfg.normalize()
	return cfg
}

// GetConfig 返回当前配置。
func (s *Service) GetConfig(ctx context.Context) Config {
	return s.loadConfig(ctx)
}

// SaveConfig 校验并保存配置，返回规范化后的值。
func (s *Service) SaveConfig(ctx context.Context, cfg Config) (Config, error) {
	cfg, err := cfg.normalize()
	if err != nil {
		return cfg, err
	}
	buf, err := json.Marshal(cfg)
	if err != nil {
		return cfg, err
	}
	if err := s.prefs.Set(ctx, configPrefKey, string(buf)); err != nil {
		return cfg, err
	}
	if s.onConfig != nil {
		s.onConfig(cfg)
	}
	return cfg, nil
}

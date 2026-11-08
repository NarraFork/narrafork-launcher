package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	"narrafork-launcher/internal/utils/cryptox"
	"narrafork-launcher/internal/utils/proxyx"
)

// configPrefKey 是启动器配置在 preferences 表中的键（值为 JSON）。
const configPrefKey = "launcher.config"

// DefaultMirrorURL 是默认的 GitHub 镜像（HubProxy）：把完整的原始 URL 拼在它后面即可。
const DefaultMirrorURL = "https://hk1.aeoliancloud.com/"

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

	// DownloadSource 决定 GitHub API、校验文件与二进制从哪里取：
	// "auto" 先直连 GitHub，网络层失败时换镜像并在本次会话内记住；"github" 只直连；"mirror" 只走镜像。
	DownloadSource string `json:"downloadSource"`
	// MirrorURL 是镜像前缀，请求地址为 MirrorURL + 原始完整 URL。
	MirrorURL string `json:"mirrorUrl"`
	// DownloadProxyMode 是启动器自身下载用的代理："system" | "direct" | "custom"。
	DownloadProxyMode string `json:"downloadProxyMode"`
	// DownloadProxyURL 是 custom 模式的代理（http / https / socks5 / socks5h）。
	DownloadProxyURL string `json:"downloadProxyUrl"`
	// NarraforkProxyMode 决定写给 NarraFork 子进程的代理环境变量：
	// "inherit"（原样继承启动器的环境）| "none"（去掉代理变量）| "system" | "custom" | "download"（同下载代理）。
	NarraforkProxyMode string `json:"narraforkProxyMode"`
	// NarraforkProxyURL 是 NarraFork custom 模式的代理。
	NarraforkProxyURL string `json:"narraforkProxyUrl"`
}

func defaultConfig() Config {
	return Config{
		AutoStart:          true,
		AutoUpdate:         true,
		Channel:            "stable",
		OpenMode:           "inline",
		OpenOnReady:        true,
		CloseAction:        "minimize",
		DownloadSource:     "auto",
		MirrorURL:          DefaultMirrorURL,
		DownloadProxyMode:  "system",
		NarraforkProxyMode: "inherit",
	}
}

// ConfigError 是用户输入的配置不合法。Kind 供界面翻译：
// "invalidPort" | "invalidHost" | "invalidMirrorUrl" | "invalidProxyUrl"。
type ConfigError struct {
	Kind    string `json:"kind"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ConfigError) Error() string { return e.Message }

// normalize 校验并修正配置；非法值回落默认，返回错误只针对用户明确输入的非法端口 / 主机 / 地址。
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
	switch c.DownloadSource {
	case "auto", "github", "mirror":
	default:
		c.DownloadSource = d.DownloadSource
	}
	switch c.DownloadProxyMode {
	case "system", "direct", "custom":
	default:
		c.DownloadProxyMode = d.DownloadProxyMode
	}
	switch c.NarraforkProxyMode {
	case "inherit", "none", "system", "custom", "download":
	default:
		c.NarraforkProxyMode = d.NarraforkProxyMode
	}
	if c.Port < 0 || c.Port > 65535 {
		return c, &ConfigError{Kind: "invalidPort", Field: "port", Message: fmt.Sprintf("invalid port %d", c.Port)}
	}
	c.Host = strings.TrimSpace(c.Host)
	if c.Host != "" && !validHost(c.Host) {
		return c, &ConfigError{Kind: "invalidHost", Field: "host", Message: fmt.Sprintf("invalid host %q", c.Host)}
	}
	c.BinaryPath = strings.TrimSpace(c.BinaryPath)

	mirror, err := normalizeMirrorURL(c.MirrorURL)
	if err != nil {
		return c, &ConfigError{Kind: "invalidMirrorUrl", Field: "mirrorUrl", Message: err.Error()}
	}
	c.MirrorURL = mirror
	// 代理地址：只校验当前模式会用到的那个；空地址的 custom 视为未填完，运行时按配置错误失败（不悄悄直连）。
	if c.DownloadProxyURL, err = normalizeProxyURL(c.DownloadProxyURL); err != nil {
		return c, &ConfigError{Kind: "invalidProxyUrl", Field: "downloadProxyUrl", Message: "download proxy: " + err.Error()}
	}
	if c.NarraforkProxyURL, err = normalizeProxyURL(c.NarraforkProxyURL); err != nil {
		return c, &ConfigError{Kind: "invalidProxyUrl", Field: "narraforkProxyUrl", Message: "NarraFork proxy: " + err.Error()}
	}
	return c, nil
}

// normalizeMirrorURL：空串回落默认；必须是 http(s)://host[/path]，结尾补 "/"。
func normalizeMirrorURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultMirrorURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("mirror URL must be an http:// or https:// address: %q", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("mirror URL must not contain credentials, a query or a fragment: %q", raw)
	}
	s := u.String()
	if !strings.HasSuffix(s, "/") {
		s += "/"
	}
	return s, nil
}

func normalizeProxyURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := proxyx.ParseURL(raw)
	if err != nil {
		return "", err
	}
	return u.String(), nil
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
	if s.prefs == nil {
		// 无持久化（单元测试 / E2E）：用内存里的配置。
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.memConfig != nil {
			return *s.memConfig
		}
		return defaultConfig()
	}
	raw, err := s.prefs.Get(ctx, configPrefKey)
	cfg := defaultConfig()
	if err != nil || raw == "" {
		return cfg
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		s.log.Warn("launcher config corrupt; using defaults", "err", err)
		return defaultConfig()
	}
	cfg.DownloadProxyURL = s.openSecret(cfg.DownloadProxyURL)
	cfg.NarraforkProxyURL = s.openSecret(cfg.NarraforkProxyURL)
	cfg, _ = cfg.normalize()
	return cfg
}

// GetConfig 返回当前配置。
func (s *Service) GetConfig(ctx context.Context) Config {
	return s.loadConfig(ctx)
}

// SaveConfig 校验并保存配置，返回规范化后的值。下载源 / 代理变化后，下一个请求即使用新的 client。
func (s *Service) SaveConfig(ctx context.Context, cfg Config) (Config, error) {
	cfg, err := cfg.normalize()
	if err != nil {
		return cfg, err
	}
	if s.prefs == nil {
		s.mu.Lock()
		saved := cfg
		s.memConfig = &saved
		s.mu.Unlock()
	} else {
		stored := cfg
		if stored.DownloadProxyURL, err = sealSecret(stored.DownloadProxyURL); err != nil {
			return cfg, err
		}
		if stored.NarraforkProxyURL, err = sealSecret(stored.NarraforkProxyURL); err != nil {
			return cfg, err
		}
		buf, err := json.Marshal(stored)
		if err != nil {
			return cfg, err
		}
		if err := s.prefs.Set(ctx, configPrefKey, string(buf)); err != nil {
			return cfg, err
		}
	}
	s.net.sync(cfg)
	if s.onConfig != nil {
		s.onConfig(cfg)
	}
	return cfg, nil
}

// sealedPrefix 标记经 cryptox 加密的配置值。代理地址可能带 user:pass，落 SQLite 前加密。
const sealedPrefix = "enc:v1:"

func sealSecret(plain string) (string, error) {
	u, err := url.Parse(plain)
	if plain == "" || err != nil || u.User == nil {
		return plain, nil
	}
	if _, hasPass := u.User.Password(); !hasPass {
		return plain, nil
	}
	enc, err := cryptox.EncryptString(plain)
	if err != nil {
		return "", fmt.Errorf("encrypt proxy credentials: %w", err)
	}
	return sealedPrefix + enc, nil
}

func (s *Service) openSecret(stored string) string {
	enc, ok := strings.CutPrefix(stored, sealedPrefix)
	if !ok {
		return stored
	}
	plain, err := cryptox.DecryptString(enc)
	if err != nil {
		s.log.Warn("cannot decrypt saved proxy URL; clearing it", "err", err)
		return ""
	}
	return plain
}

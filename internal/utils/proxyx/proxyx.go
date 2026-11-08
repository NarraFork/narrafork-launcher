// Package proxyx 解析出站代理：系统代理（环境变量 / Windows 注册表）、自定义代理 URL 与绕过列表。
//
// 设计要点：
//   - 系统代理先看环境变量 HTTPS_PROXY → HTTP_PROXY → ALL_PROXY（含小写），与 NarraFork
//     server/lib/net/proxy-env.ts 的优先级一致；没有时在 Windows 上读 Internet Settings 注册表
//   - 回环地址（localhost / *.localhost / 127.0.0.0/8 / ::1）永远直连
//   - 支持 http / https / socks5 / socks5h 代理（net/http.Transport 原生支持）
//
// 用法：
//
//	st := proxyx.System()              // 或 proxyx.Custom(u)
//	client := httpx.NewClient(httpx.ClientOptions{Proxy: st.Func()})
package proxyx

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Origin 说明代理配置的来源。
type Origin string

const (
	OriginNone     Origin = ""
	OriginEnv      Origin = "env"
	OriginRegistry Origin = "registry"
	OriginCustom   Origin = "custom"
)

// Settings 是一份解析好的代理配置。零值表示直连。
type Settings struct {
	Origin Origin
	// HTTP / HTTPS 分别用于 http:// 与 https:// 目标；为 nil 时该协议直连。
	HTTP  *url.URL
	HTTPS *url.URL
	// Bypass 是绕过条目：NO_PROXY 风格（example.com / .example.com / 10.0.0.0/8 / *）
	// 或注册表 ProxyOverride 风格（*.example.com / 192.168.*）。
	Bypass []string
	// BypassLocal 对应注册表的 <local>：不含点的主机名直连。
	BypassLocal bool
}

// Enabled 报告是否有任一协议走代理。
func (s Settings) Enabled() bool { return s.HTTP != nil || s.HTTPS != nil }

// Primary 返回代表性的代理（优先 HTTPS），用于展示与传给子进程。
func (s Settings) Primary() *url.URL {
	if s.HTTPS != nil {
		return s.HTTPS
	}
	return s.HTTP
}

// ProxyFor 返回 target 应使用的代理；nil 表示直连。
func (s Settings) ProxyFor(target *url.URL) *url.URL {
	if target == nil || !s.Enabled() {
		return nil
	}
	if Bypassed(target.Hostname(), s.Bypass, s.BypassLocal) {
		return nil
	}
	if strings.EqualFold(target.Scheme, "http") {
		return s.HTTP
	}
	return s.HTTPS
}

// Func 返回可直接放进 http.Transport.Proxy 的函数。
func (s Settings) Func() func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) { return s.ProxyFor(req.URL), nil }
}

// Custom 用单个代理 URL 构造配置（只绕过回环地址）。
func Custom(u *url.URL) Settings {
	if u == nil {
		return Settings{}
	}
	return Settings{Origin: OriginCustom, HTTP: u, HTTPS: u}
}

// ParseURL 校验并规范化代理地址。没写协议时按 http 处理（"127.0.0.1:7890"）。
// 支持 http / https / socks5 / socks5h，可带 user:pass@。
func ParseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty proxy URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q (use http, https, socks5 or socks5h)", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, errors.New("proxy URL has no host")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n <= 0 || n > 65535 {
			return nil, fmt.Errorf("invalid proxy port %q", p)
		}
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("proxy URL must not contain a path, query or fragment")
	}
	u.Path = ""
	return u, nil
}

// Redact 返回隐藏密码的展示串；nil 返回空串。
func Redact(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Redacted()
}

// —— 系统代理 ——

// envKeys 是 HTTPS → HTTP → ALL 的读取顺序（与 NarraFork ambientSystemProxy 一致）。
var envKeys = []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"}

// FromEnv 从环境变量解析代理；getenv 为 nil 时用 os.Getenv。非法值忽略。
func FromEnv(getenv func(string) string) Settings {
	if getenv == nil {
		getenv = os.Getenv
	}
	var proxy *url.URL
	for _, k := range envKeys {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			if u, err := ParseURL(v); err == nil {
				proxy = u
				break
			}
		}
	}
	if proxy == nil {
		return Settings{}
	}
	noProxy := getenv("NO_PROXY")
	if noProxy == "" {
		noProxy = getenv("no_proxy")
	}
	return Settings{Origin: OriginEnv, HTTP: proxy, HTTPS: proxy, Bypass: splitList(noProxy, ",")}
}

// ParseWinINet 解析 Internet Settings 的 ProxyEnable / ProxyServer / ProxyOverride。
//
// ProxyServer 有两种形态："host:port"（所有协议共用）或 "http=h:p;https=h:p;socks=h:p"。
// socks= 映射为 socks5://（Go 只支持 SOCKS5；Clash 等常见混合端口两者都支持）。
func ParseWinINet(enable uint32, server, override string) Settings {
	server = strings.TrimSpace(server)
	if enable == 0 || server == "" {
		return Settings{}
	}
	st := Settings{Origin: OriginRegistry}
	if !strings.Contains(server, "=") {
		if u, err := ParseURL(server); err == nil {
			st.HTTP, st.HTTPS = u, u
		}
	} else {
		byProto := map[string]*url.URL{}
		for _, part := range splitList(server, ";") {
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			k = strings.ToLower(strings.TrimSpace(k))
			v = strings.TrimSpace(v)
			if k == "socks" && !strings.Contains(v, "://") {
				v = "socks5://" + v
			}
			if u, err := ParseURL(v); err == nil {
				byProto[k] = u
			}
		}
		st.HTTP = firstURL(byProto["http"], byProto["socks"])
		st.HTTPS = firstURL(byProto["https"], byProto["socks"])
	}
	if !st.Enabled() {
		return Settings{}
	}
	for _, e := range splitList(override, ";") {
		if strings.EqualFold(e, "<local>") {
			st.BypassLocal = true
			continue
		}
		st.Bypass = append(st.Bypass, e)
	}
	return st
}

func firstURL(us ...*url.URL) *url.URL {
	for _, u := range us {
		if u != nil {
			return u
		}
	}
	return nil
}

var (
	sysMu   sync.Mutex
	sysAt   time.Time
	sysLast Settings
)

// systemCacheTTL：注册表结果缓存时长。用户在 Clash 等工具里切换系统代理后，几秒内即可生效。
const systemCacheTTL = 5 * time.Second

// System 返回当前系统代理：环境变量优先，其次 Windows 注册表（其它平台无注册表来源）。
func System() Settings {
	if st := FromEnv(nil); st.Enabled() {
		return st
	}
	sysMu.Lock()
	defer sysMu.Unlock()
	if !sysAt.IsZero() && time.Since(sysAt) < systemCacheTTL {
		return sysLast
	}
	sysLast = readRegistry()
	sysAt = time.Now()
	return sysLast
}

// —— 绕过规则 ——

// IsLoopback 判断 localhost / *.localhost / 回环 IP。
func IsLoopback(host string) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Bypassed 判断 host 是否应直连：回环地址总是直连，其余按绕过列表匹配。
func Bypassed(host string, entries []string, local bool) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))
	if h == "" || IsLoopback(h) {
		return true
	}
	ip := net.ParseIP(h)
	if local && ip == nil && !strings.Contains(h, ".") {
		return true
	}
	for _, raw := range entries {
		e := strings.ToLower(strings.TrimSpace(raw))
		if e == "" {
			continue
		}
		if e == "*" {
			return true
		}
		if _, n, err := net.ParseCIDR(e); err == nil {
			if ip != nil && n.Contains(ip) {
				return true
			}
			continue
		}
		e = stripPort(e)
		if strings.Contains(e, "*") {
			if wildcard(e, h) {
				return true
			}
			continue
		}
		if bare, ok := strings.CutPrefix(e, "."); ok {
			if h == bare || strings.HasSuffix(h, e) {
				return true
			}
			continue
		}
		if h == e || (ip == nil && strings.HasSuffix(h, "."+e)) {
			return true
		}
	}
	return false
}

func stripPort(e string) string {
	if strings.HasPrefix(e, "[") {
		if i := strings.Index(e, "]"); i > 0 {
			return e[1:i]
		}
		return e
	}
	if strings.Count(e, ":") == 1 {
		h, _, _ := strings.Cut(e, ":")
		return h
	}
	return e
}

// wildcard 匹配含 * 的模式（* 匹配任意字符，包括点）。
func wildcard(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := len(parts) - 1
	for i := 1; i < last; i++ {
		idx := strings.Index(s, parts[i])
		if idx < 0 {
			return false
		}
		s = s[idx+len(parts[i]):]
	}
	return strings.HasSuffix(s, parts[last])
}

// —— 子进程环境 ——

// EnvKeys 是子进程识别的代理变量（大小写两套），与 NarraFork proxy-env.ts 一致。
var EnvKeys = []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"}

// NoProxyList 生成 NO_PROXY 值：回环地址 + 能用 NO_PROXY 语法表达的绕过条目。
// 注册表通配 "*.example.com" → ".example.com"，"192.168.*" → "192.168.0.0/16"；
// <local> 与其它无法表达的通配条目跳过。
func NoProxyList(s Settings) string {
	out := []string{"localhost", "127.0.0.1", "::1"}
	seen := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	for _, raw := range s.Bypass {
		e, ok := noProxyEntry(strings.ToLower(strings.TrimSpace(raw)))
		if !ok || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return strings.Join(out, ",")
}

func noProxyEntry(e string) (string, bool) {
	if e == "" || strings.ContainsAny(e, " ,;") {
		return "", false
	}
	if !strings.Contains(e, "*") {
		return e, true
	}
	if e == "*" {
		return e, true
	}
	if rest, ok := strings.CutPrefix(e, "*."); ok && !strings.Contains(rest, "*") {
		return "." + rest, true
	}
	// 数字前缀通配：10.* / 172.16.* / 192.168.1.*
	if prefix, ok := strings.CutSuffix(e, ".*"); ok {
		octets := strings.Split(prefix, ".")
		if len(octets) >= 1 && len(octets) <= 3 {
			ip := make([]string, 4)
			for i := range ip {
				ip[i] = "0"
			}
			for i, o := range octets {
				n, err := strconv.Atoi(o)
				if err != nil || n < 0 || n > 255 {
					return "", false
				}
				ip[i] = strconv.Itoa(n)
			}
			return strings.Join(ip, ".") + "/" + strconv.Itoa(8*len(octets)), true
		}
	}
	return "", false
}

// EnvVars 返回要写入子进程的代理变量（大小写两套）。s 未启用时返回 nil。
func EnvVars(s Settings) map[string]string {
	if !s.Enabled() {
		return nil
	}
	httpP, httpsP := s.HTTP, s.HTTPS
	if httpP == nil {
		httpP = httpsP
	}
	if httpsP == nil {
		httpsP = httpP
	}
	noProxy := NoProxyList(s)
	return map[string]string{
		"HTTP_PROXY": httpP.String(), "http_proxy": httpP.String(),
		"HTTPS_PROXY": httpsP.String(), "https_proxy": httpsP.String(),
		"ALL_PROXY": httpsP.String(), "all_proxy": httpsP.String(),
		"NO_PROXY": noProxy, "no_proxy": noProxy,
	}
}

func splitList(s, sep string) []string {
	var out []string
	for _, p := range strings.Split(s, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

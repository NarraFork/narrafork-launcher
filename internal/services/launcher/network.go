package launcher

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"narrafork-launcher/internal/utils/httpx"
	"narrafork-launcher/internal/utils/proxyx"
)

// 下载源。"auto" 是配置值，不是源。
const (
	srcGitHub = "github"
	srcMirror = "mirror"
)

// 网络操作，出现在 NetError.Op 中。
const (
	opAPI      = "api"
	opChecksum = "checksum"
	opDownload = "download"
	opTest     = "test"
)

const (
	// apiCacheTTL：发布信息缓存时长。匿名 GitHub API（以及镜像自己的限额）每小时只有 60 次。
	apiCacheTTL = 5 * time.Minute
	// autoDialTimeout：auto 模式直连 GitHub 时的建连超时，连不上尽快换镜像。
	autoDialTimeout = 8 * time.Second
	// dialTimeout：其余情况的建连 / TLS 握手超时。
	dialTimeout = 15 * time.Second
	// apiAttemptTimeout：单次 API / 校验文件请求在一个源上的总时长。
	apiAttemptTimeout = 20 * time.Second
)

// NetError 是一次网络操作的失败。经 Wails 返回时整个结构体序列化到 RuntimeError.cause，
// 前端按 Kind 翻译：
// "dns" | "timeout" | "refused" | "reset" | "tls" | "proxy" | "network" | "stalled" |
// "http" | "rateLimit" | "badResponse" | "checksum" | "config" | "cancelled"。
type NetError struct {
	Kind    string `json:"kind"`
	Op      string `json:"op"`     // "api" | "checksum" | "download" | "test"
	Source  string `json:"source"` // "github" | "mirror"；与源无关时为空
	Host    string `json:"host"`
	Status  int    `json:"status"`
	Message string `json:"message"`
	err     error
	// preResponse：失败发生在拿到响应之前（建连 / TLS / 代理 / 等响应头）。
	preResponse bool
}

func (e *NetError) Error() string { return e.Message }
func (e *NetError) Unwrap() error { return e.err }

// errStalled 是下载在 stallTimeout 内没有收到任何字节。
var errStalled = errors.New("no data received for too long")

var opLabels = map[string]string{
	opAPI: "release info request", opChecksum: "checksum download", opDownload: "download", opTest: "connection test",
}

var sourceLabels = map[string]string{srcGitHub: "GitHub", srcMirror: "mirror"}

func newNetError(op, src, target string, err error) *NetError {
	var ne *NetError
	if errors.As(err, &ne) {
		return ne
	}
	kind := classifyErr(err)
	raw := strings.TrimPrefix(err.Error(), "httpx: do request: ")
	return &NetError{
		Kind:    kind,
		Op:      op,
		Source:  src,
		Host:    hostOf(target),
		Message: fmt.Sprintf("%s via %s failed (%s): %s", opLabels[op], sourceLabel(src), kind, raw),
		err:     err,
	}
}

func httpStatusError(op, src, target string, resp *http.Response) *NetError {
	kind := "http"
	if resp.StatusCode == http.StatusTooManyRequests ||
		(resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0") {
		kind = "rateLimit"
	}
	msg := fmt.Sprintf("%s via %s failed: HTTP %d from %s", opLabels[op], sourceLabel(src), resp.StatusCode, hostOf(target))
	if kind == "rateLimit" {
		msg += " (API rate limit reached; try again later, switch the download source, or set GITHUB_TOKEN)"
	}
	return &NetError{Kind: kind, Op: op, Source: src, Host: hostOf(target), Status: resp.StatusCode, Message: msg}
}

func sourceLabel(src string) string {
	if l, ok := sourceLabels[src]; ok {
		return l
	}
	return "network"
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
}

// windowsNetErrnos 是 Winsock 错误码（WSAETIMEDOUT 等）。Go 不把它们映射到 syscall.ETIMEDOUT 等常量，
// 按数值判断；这些数值与 Unix errno 不冲突。
var windowsNetErrnos = map[uintptr]string{
	10060: "timeout", 10061: "refused", 10054: "reset", 10053: "reset", 10051: "network", 10065: "network",
}

// classifyErr 把底层错误归为 NetError.Kind。
func classifyErr(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errStalled):
		return "stalled"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && (opErr.Op == "proxyconnect" || strings.HasPrefix(opErr.Op, "socks")) {
		return "proxy"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var (
		certErr    *tls.CertificateVerificationError
		unknownCA  x509.UnknownAuthorityError
		hostErr    x509.HostnameError
		recordErr  tls.RecordHeaderError
		alertErr   tls.AlertError
		invalidErr x509.CertificateInvalidError
	)
	if errors.As(err, &certErr) || errors.As(err, &unknownCA) || errors.As(err, &hostErr) ||
		errors.As(err, &recordErr) || errors.As(err, &alertErr) || errors.As(err, &invalidErr) {
		return "tls"
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if k, ok := windowsNetErrnos[uintptr(errno)]; ok {
			return k
		}
		switch {
		case errors.Is(errno, syscall.ECONNREFUSED):
			return "refused"
		case errors.Is(errno, syscall.ECONNRESET), errors.Is(errno, syscall.ECONNABORTED):
			return "reset"
		case errors.Is(errno, syscall.ETIMEDOUT):
			return "timeout"
		}
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return "timeout"
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return "reset"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:") || strings.Contains(msg, "certificate"):
		return "tls"
	case strings.Contains(msg, "refused"):
		return "refused"
	case strings.Contains(msg, "reset") || strings.Contains(msg, "forcibly closed") || strings.Contains(msg, "broken pipe"):
		return "reset"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out"):
		return "timeout"
	}
	return "network"
}

// fallbackKind 报告这类失败是否是网络层的：auto 模式下换源重试，下载时可续传重试。HTTP 4xx 等不算。
func fallbackKind(kind string) bool {
	switch kind {
	case "dns", "timeout", "refused", "reset", "tls", "proxy", "network", "stalled":
		return true
	}
	return false
}

func errKind(err error) string {
	var ne *NetError
	if errors.As(err, &ne) {
		return ne.Kind
	}
	return classifyErr(err)
}

// —— 网络管理 ——

type clientKey struct {
	dial, header time.Duration
	direct       bool // 不走任何代理（连通性测试用）
	noRedirect   bool
}

type cacheEntry struct {
	body []byte
	src  string
	at   time.Time
}

// netManager 负责下载源选择（auto 回退 + 会话内记忆）、按代理配置缓存 http.Client、发布信息缓存。
type netManager struct {
	notify func(string) // 写一行 system 日志

	mu         sync.Mutex
	proxyKey   string
	clients    map[clientKey]*http.Client
	remembered string // auto 模式下本次会话确认可用的源
	lastSource string // 最近一次成功请求使用的源
	cfgKey     string
	cache      map[string]cacheEntry
}

func newNetManager(notify func(string)) *netManager {
	return &netManager{notify: notify, clients: map[clientKey]*http.Client{}, cache: map[string]cacheEntry{}}
}

func (n *netManager) say(msg string) {
	if n.notify != nil {
		n.notify(msg)
	}
}

// sync 在配置保存后调用：下载源 / 镜像 / 代理变化时忘掉 auto 模式记住的源（新代理也许能直连 GitHub 了）。
func (n *netManager) sync(cfg Config) {
	key := strings.Join([]string{cfg.DownloadSource, cfg.MirrorURL, cfg.DownloadProxyMode, cfg.DownloadProxyURL}, "\x00")
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cfgKey != "" && n.cfgKey != key {
		n.remembered = ""
	}
	n.cfgKey = key
}

// order 返回本次请求依次尝试的源。
func (n *netManager) order(cfg Config) []string {
	switch cfg.DownloadSource {
	case srcGitHub:
		return []string{srcGitHub}
	case srcMirror:
		return []string{srcMirror}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.remembered == srcMirror {
		return []string{srcMirror, srcGitHub}
	}
	return []string{srcGitHub, srcMirror}
}

// remember 记录成功的源；auto 模式下首次换到镜像（或换回 GitHub）时写日志。
func (n *netManager) remember(cfg Config, src string) {
	n.mu.Lock()
	changed := cfg.DownloadSource == "auto" && n.remembered != src && !(n.remembered == "" && src == srcGitHub)
	n.lastSource = src
	if cfg.DownloadSource == "auto" {
		n.remembered = src
	}
	n.mu.Unlock()
	if changed {
		n.say(fmt.Sprintf("Download source: using %s for the rest of this session", describeSource(cfg, src)))
	}
}

func describeSource(cfg Config, src string) string {
	if src == srcMirror {
		return "mirror " + hostOf(cfg.MirrorURL)
	}
	return "GitHub"
}

// mirrored 把原始 GitHub URL 换成要请求的地址：镜像 = 前缀 + 完整原始 URL。
func mirrored(cfg Config, src, raw string) string {
	if src == srcMirror {
		return cfg.MirrorURL + raw
	}
	return raw
}

// do 在候选源上依次执行 fn：auto 模式下网络层失败换下一个源；成功后记住该源。
func (n *netManager) do(ctx context.Context, cfg Config, fn func(ctx context.Context, src string) error) (string, error) {
	order := n.order(cfg)
	var last error
	for i, src := range order {
		err := fn(ctx, src)
		if err == nil {
			n.remember(cfg, src)
			return src, nil
		}
		last = err
		if ctx.Err() != nil || i == len(order)-1 || !fallbackKind(errKind(err)) {
			return src, err
		}
		n.say(fmt.Sprintf("%s; trying %s", err.Error(), describeSource(cfg, order[i+1])))
	}
	return "", last
}

// proxySettings 解析下载代理。custom 模式没填地址时报配置错误，不悄悄直连。
func (n *netManager) proxySettings(cfg Config) (proxyx.Settings, error) {
	switch cfg.DownloadProxyMode {
	case "direct":
		return proxyx.Settings{}, nil
	case "custom":
		if cfg.DownloadProxyURL == "" {
			return proxyx.Settings{}, &NetError{Kind: "config", Message: "download proxy is set to custom but no proxy URL is configured"}
		}
		u, err := proxyx.ParseURL(cfg.DownloadProxyURL)
		if err != nil {
			return proxyx.Settings{}, &NetError{Kind: "config", Message: "download proxy: " + err.Error(), err: err}
		}
		return proxyx.Custom(u), nil
	default:
		return proxyx.System(), nil
	}
}

// client 返回按当前代理配置缓存的 client；代理配置变化时丢弃旧 client（关闭空闲连接）。
func (n *netManager) client(cfg Config, key clientKey) (*http.Client, error) {
	st := proxyx.Settings{}
	if !key.direct {
		var err error
		if st, err = n.proxySettings(cfg); err != nil {
			return nil, err
		}
	}
	fp := fmt.Sprint(st.HTTP, "|", st.HTTPS, "|", st.Bypass, "|", st.BypassLocal)
	n.mu.Lock()
	defer n.mu.Unlock()
	if fp != n.proxyKey {
		for k, c := range n.clients {
			if !k.direct {
				c.CloseIdleConnections()
				delete(n.clients, k)
			}
		}
		n.proxyKey = fp
	}
	if c := n.clients[key]; c != nil {
		return c, nil
	}
	opts := httpx.ClientOptions{DialTimeout: key.dial, ResponseHeaderTimeout: key.header, NoRedirect: key.noRedirect}
	if !key.direct && st.Enabled() {
		opts.Proxy = st.Func()
	}
	c := httpx.NewClient(opts)
	n.clients[key] = c
	return c, nil
}

// dialFor：auto 模式直连 GitHub 用短超时，连不上尽快换镜像。
func dialFor(cfg Config, src string) time.Duration {
	if cfg.DownloadSource == "auto" && src == srcGitHub {
		return autoDialTimeout
	}
	return dialTimeout
}

// fetch 在指定源上 GET 一个小响应（API JSON / SHA256SUMS），4xx/5xx 返回 NetError。
func (n *netManager) fetch(ctx context.Context, cfg Config, src, op, raw string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, apiAttemptTimeout)
	defer cancel()
	target := mirrored(cfg, src, raw)
	c, err := n.client(cfg, clientKey{dial: dialFor(cfg, src), header: apiAttemptTimeout})
	if err != nil {
		return nil, err
	}
	req, reqCancel, err := httpx.NewRequest(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	defer reqCancel()
	if op == opAPI {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		// 令牌只发给 GitHub 本身，绝不发给第三方镜像。
		if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" && src == srcGitHub && req.URL.Host == "api.github.com" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := httpx.DoWith(c, req)
	if err != nil {
		return nil, newNetError(op, src, target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, httpStatusError(op, src, target, resp)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, newNetError(op, src, target, err)
	}
	return body, nil
}

// getAPI 读取 GitHub API（带 5 分钟缓存），返回实际使用的源与是否命中缓存。
func (n *netManager) getAPI(ctx context.Context, cfg Config, apiURL string, out any) (string, bool, error) {
	n.mu.Lock()
	entry, ok := n.cache[apiURL]
	n.mu.Unlock()
	if ok && time.Since(entry.at) < apiCacheTTL {
		if err := json.Unmarshal(entry.body, out); err == nil {
			return entry.src, true, nil
		}
	}
	var body []byte
	src, err := n.do(ctx, cfg, func(ctx context.Context, src string) error {
		b, err := n.fetch(ctx, cfg, src, opAPI, apiURL, 16<<20)
		body = b
		return err
	})
	if err != nil {
		return src, false, err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return src, false, &NetError{
			Kind: "badResponse", Op: opAPI, Source: src, Host: hostOf(mirrored(cfg, src, apiURL)),
			Message: fmt.Sprintf("release info via %s is not valid JSON: %v", sourceLabel(src), err), err: err,
		}
	}
	n.mu.Lock()
	n.cache[apiURL] = cacheEntry{body: body, src: src, at: time.Now()}
	n.mu.Unlock()
	return src, false, nil
}

// —— 状态与连通性测试 ——

// NetworkStatus 描述下载网络配置与本次会话的实际情况。
type NetworkStatus struct {
	DownloadSource string `json:"downloadSource"` // 配置："auto" | "github" | "mirror"
	ActiveSource   string `json:"activeSource"`   // 下一次请求首先使用的源
	LastSource     string `json:"lastSource"`     // 最近一次成功请求使用的源；尚无为空
	MirrorURL      string `json:"mirrorUrl"`
	ProxyMode      string `json:"proxyMode"`
	Proxy          string `json:"proxy"`       // 下载实际使用的代理（密码已隐藏）；直连为空
	ProxyOrigin    string `json:"proxyOrigin"` // "env" | "registry" | "custom" | ""
	ProxyError     string `json:"proxyError"`
	// SystemProxy / SystemProxyOrigin 是检测到的系统代理（与当前模式无关），供界面提示。
	SystemProxy       string `json:"systemProxy"`
	SystemProxyOrigin string `json:"systemProxyOrigin"`
}

func (n *netManager) status(cfg Config) NetworkStatus {
	st := NetworkStatus{
		DownloadSource: cfg.DownloadSource,
		ActiveSource:   n.order(cfg)[0],
		MirrorURL:      cfg.MirrorURL,
		ProxyMode:      cfg.DownloadProxyMode,
	}
	n.mu.Lock()
	st.LastSource = n.lastSource
	n.mu.Unlock()
	if p, err := n.proxySettings(cfg); err != nil {
		st.ProxyError = err.Error()
	} else if p.Enabled() {
		st.Proxy = proxyx.Redact(p.Primary())
		st.ProxyOrigin = string(p.Origin)
	}
	if sys := proxyx.System(); sys.Enabled() {
		st.SystemProxy = proxyx.Redact(sys.Primary())
		st.SystemProxyOrigin = string(sys.Origin)
	}
	return st
}

// NetworkTestResult 是一个测试目标的结果。
type NetworkTestResult struct {
	ID        string `json:"id"`    // "githubApi" | "githubDownload" | "mirror" | "proxy"
	URL       string `json:"url"`   // 实际请求的地址
	Proxy     string `json:"proxy"` // 经过的代理（密码已隐藏）；直连为空
	OK        bool   `json:"ok"`
	Skipped   bool   `json:"skipped"` // proxy：当前没有生效的下载代理
	Status    int    `json:"status"`
	LatencyMs int64  `json:"latencyMs"`
	Error     string `json:"error"`
	ErrorKind string `json:"errorKind"`
}

const (
	testLatestAPI  = apiBase + "/releases/latest"
	testRateLimit  = "https://api.github.com/rate_limit" // 查询本身不计入限额
	testReleaseURL = "https://github.com/" + Repository + "/releases/latest"
)

// test 并发测试：GitHub API 与 github.com（下载域名）直连、镜像（按下载代理配置）、经下载代理访问 github.com。
func (n *netManager) test(ctx context.Context, cfg Config) []NetworkTestResult {
	type target struct {
		id, method, url string
		direct          bool
	}
	targets := []target{
		{"githubApi", http.MethodGet, testRateLimit, true},
		{"githubDownload", http.MethodHead, testReleaseURL, true},
		{"mirror", http.MethodHead, cfg.MirrorURL + testLatestAPI, false},
		{"proxy", http.MethodHead, testReleaseURL, false},
	}
	proxy, proxyErr := n.proxySettings(cfg)
	out := make([]NetworkTestResult, len(targets))
	var wg sync.WaitGroup
	for i, tg := range targets {
		res := NetworkTestResult{ID: tg.id, URL: tg.url}
		if !tg.direct && proxyErr == nil && proxy.Enabled() {
			res.Proxy = proxyx.Redact(proxy.ProxyFor(mustURL(tg.url)))
		}
		if tg.id == "proxy" && proxyErr == nil && res.Proxy == "" {
			res.Skipped = true
			out[i] = res
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = n.probe(ctx, cfg, res, tg.method, tg.direct)
		}()
	}
	wg.Wait()
	return out
}

func mustURL(raw string) *url.URL {
	u, _ := url.Parse(raw)
	return u
}

func (n *netManager) probe(ctx context.Context, cfg Config, res NetworkTestResult, method string, direct bool) NetworkTestResult {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	fail := func(err error) NetworkTestResult {
		ne := newNetError(opTest, "", res.URL, err)
		res.ErrorKind, res.Error = ne.Kind, strings.TrimPrefix(err.Error(), "httpx: do request: ")
		return res
	}
	c, err := n.client(cfg, clientKey{dial: autoDialTimeout, header: 10 * time.Second, direct: direct, noRedirect: true})
	if err != nil {
		return fail(err)
	}
	req, reqCancel, err := httpx.NewRequest(ctx, method, res.URL, nil)
	if err != nil {
		return fail(err)
	}
	defer reqCancel()
	begin := time.Now()
	resp, err := httpx.DoWith(c, req)
	res.LatencyMs = time.Since(begin).Milliseconds()
	if err != nil {
		return fail(err)
	}
	_ = resp.Body.Close()
	res.Status = resp.StatusCode
	if resp.StatusCode >= 400 {
		ne := httpStatusError(opTest, "", res.URL, resp)
		res.ErrorKind, res.Error = ne.Kind, ne.Message
		return res
	}
	res.OK = true
	return res
}

// —— NarraFork 子进程的代理环境 ——

// childEnv 按 NarraforkProxyMode 生成子进程环境，返回给日志看的说明（空 = 无需说明）。
func (s *Service) childEnv(cfg Config) ([]string, string, error) {
	base := os.Environ()
	var st proxyx.Settings
	switch cfg.NarraforkProxyMode {
	case "none":
		return stripProxyEnv(base), "NarraFork proxy: none (proxy variables removed from its environment)", nil
	case "system":
		st = proxyx.System()
	case "custom":
		if cfg.NarraforkProxyURL == "" {
			return nil, "", &ConfigError{Kind: "invalidProxyUrl", Field: "narraforkProxyUrl", Message: "NarraFork proxy is set to custom but no proxy URL is configured"}
		}
		u, err := proxyx.ParseURL(cfg.NarraforkProxyURL)
		if err != nil {
			return nil, "", &ConfigError{Kind: "invalidProxyUrl", Field: "narraforkProxyUrl", Message: "NarraFork proxy: " + err.Error()}
		}
		st = proxyx.Custom(u)
	case "download":
		var err error
		if st, err = s.net.proxySettings(cfg); err != nil {
			return nil, "", err
		}
	default: // inherit
		return base, "", nil
	}
	env := stripProxyEnv(base)
	if !st.Enabled() {
		return env, "NarraFork proxy: no proxy configured for mode " + cfg.NarraforkProxyMode + "; proxy variables removed", nil
	}
	vars := proxyx.EnvVars(st)
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+vars[k])
	}
	note := "NarraFork proxy: " + proxyx.Redact(st.Primary()) + " (NO_PROXY=" + vars["NO_PROXY"] + ")"
	if p := st.Primary(); p != nil && strings.HasPrefix(p.Scheme, "socks") {
		note += "; NarraFork itself only supports HTTP(S) proxies, SOCKS applies to git and other subprocesses"
	}
	return env, note, nil
}

// stripProxyEnv 去掉 HTTP(S)_PROXY / ALL_PROXY / NO_PROXY（任意大小写）。
func stripProxyEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key := kv
		// Windows 有 "=C:=C:\\" 这类以 = 开头的条目，键从第二个字符开始找 '='。
		if i := strings.IndexByte(kv[min(1, len(kv)):], '='); i >= 0 {
			key = kv[:i+min(1, len(kv))]
		}
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		out = append(out, kv)
	}
	return out
}

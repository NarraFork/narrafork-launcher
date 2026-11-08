package launcher

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"narrafork-launcher/internal/utils/procx"
)

// managerTokenEnv / managedEnv 与 NarraFork server/lib/manager-token.ts、backend-process.ts 一致：
//   - NARRAFORK_MANAGER_MANAGED=1：由外部托管，服务不再自行打开浏览器，实例锁冲突时报错而不是静默退出
//   - NARRAFORK_MANAGER_TOKEN：回环地址 + Bearer 令牌即可调用 /api/system/lifecycle/signal 优雅停机
const (
	managedEnv      = "NARRAFORK_MANAGER_MANAGED"
	managerTokenEnv = "NARRAFORK_MANAGER_TOKEN"
)

// 与 NarraFork server/manager/listen-log.ts 的解析规则一致。
var (
	addressLine = regexp.MustCompile(`(running|startup page) on (https?)://(\[[^\]]+\]|[^:/\s"]+):(\d+)`)
	lockLine    = regexp.MustCompile(`Another NarraFork process \(pid (\d+)\) is already using`)
)

// runState 描述一次启动。
type runState struct {
	pid       int
	binary    string
	version   string
	token     string
	startedAt time.Time

	mu        sync.Mutex
	protocol  string
	host      string
	port      int
	listening bool // 已看到 `running on`（不是启动状态页）
	// adopted 是就绪探测在候选回环地址里实际连通的主机（见 waitReady），非空时健康检查与停机都用它。
	adopted  string
	lockPID  int
	stopping bool
	done     chan struct{}
	exitErr  error
}

func (r *runState) address() (protocol, host string, port int, listening bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.protocol, r.host, r.port, r.listening
}

func (r *runState) adopt(host string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adopted = host
}

func (r *runState) adoptedHost() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.adopted
}

// browserURL 是给人打开的地址：通配监听换成回环；就绪探测改用过别的回环主机时用那个。
func (r *runState) browserURL() string {
	protocol, host, port, _ := r.address()
	if port == 0 {
		return ""
	}
	h := strings.Trim(host, "[]")
	switch h {
	case "0.0.0.0", "":
		h = "127.0.0.1"
	case "::":
		h = "::1"
	}
	if a := r.adoptedHost(); a != "" {
		h = a
	}
	return protocol + "://" + net.JoinHostPort(h, strconv.Itoa(port))
}

// controlURL 是带 manager token 的控制请求（优雅停机）地址，只指向回环，规则同 NarraFork
// shared/server-host.ts 的 managerControlHost：
//   - localhost / *.localhost 原样保留：localhost 可能只解析到 ::1，服务也只听在 ::1，Go 拨号会依次尝试所有解析结果
//   - 通配 0.0.0.0 → 127.0.0.1，:: → ::1
//   - 其它主机名 / 非回环 IP → 127.0.0.1：NarraFork 此时在 127.0.0.1 同端口另开一个控制伴随监听
func (r *runState) controlURL() string {
	protocol, host, port, _ := r.address()
	if port == 0 {
		return ""
	}
	h := controlHost(host)
	if a := r.adoptedHost(); a != "" {
		h = a
	}
	return protocol + "://" + net.JoinHostPort(h, strconv.Itoa(port))
}

// healthURL 是健康检查地址，规则同 NarraFork 的 probeHost：通配地址换回环，其它主机就是公开监听本身。
// 不能像 controlURL 那样把非回环主机换成 127.0.0.1——伴随监听只放行控制接口，/api/health 会 404。
// 健康检查不带凭据，连局域网地址没有泄露风险。
func (r *runState) healthURL() string {
	protocol, host, port, _ := r.address()
	if port == 0 {
		return ""
	}
	h := strings.Trim(host, "[]")
	switch h {
	case "", "0.0.0.0":
		h = "127.0.0.1"
	case "::":
		h = "::1"
	}
	if a := r.adoptedHost(); a != "" {
		h = a
	}
	return protocol + "://" + net.JoinHostPort(h, strconv.Itoa(port))
}

func controlHost(host string) string {
	h := strings.Trim(host, "[]")
	lower := strings.ToLower(h)
	switch {
	case lower == "" || lower == "0.0.0.0":
		return "127.0.0.1"
	case lower == "::":
		return "::1"
	case isLoopbackName(lower):
		return lower
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return h
	}
	return "127.0.0.1"
}

// isLoopbackName 判断 localhost / *.localhost。
func isLoopbackName(host string) bool {
	host = strings.ToLower(host)
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

func (r *runState) observe(line string) {
	if m := lockLine.FindStringSubmatch(line); m != nil {
		pid, _ := strconv.Atoi(m[1])
		r.mu.Lock()
		r.lockPID = pid
		r.mu.Unlock()
		return
	}
	m := addressLine.FindStringSubmatch(line)
	if m == nil {
		return
	}
	port, err := strconv.Atoi(m[4])
	if err != nil || port <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.protocol != m[2] || r.host != m[3] || r.port != port {
		r.adopted = ""
	}
	r.protocol = m[2]
	r.host = m[3]
	r.port = port
	if m[1] == "running" {
		r.listening = true
	}
}

// loopbackClient 只用于访问本机 NarraFork：它可能启用了自签名证书，所以单独一个跳过校验、不走代理的 client。
// 带 token 的请求只发往回环地址（controlURL 保证）；健康检查不带凭据。
var loopbackClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		Proxy:           nil,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback only
	},
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Service) start(cfg Config, openOnReady bool) error {
	s.mu.Lock()
	if s.proc != nil {
		s.mu.Unlock()
		return nil // 已在运行
	}
	s.mu.Unlock()

	binary, version, err := s.resolveBinary(cfg)
	if err != nil {
		return err
	}
	token, err := newToken()
	if err != nil {
		return err
	}

	args := []string{"--serve"}
	if cfg.Port > 0 {
		args = append(args, "--port="+strconv.Itoa(cfg.Port))
	}
	if cfg.Host != "" {
		args = append(args, "--host="+cfg.Host)
	}
	baseEnv, proxyNote, err := s.childEnv(cfg)
	if err != nil {
		return err
	}
	env := append(baseEnv, managedEnv+"=1", managerTokenEnv+"="+token)
	s.mu.Lock()
	extraEnv := s.extraEnv
	s.mu.Unlock()
	if extraEnv != nil {
		// os/exec 对重复键取最后一个（Windows 不区分大小写），所以这里覆盖继承的 NARRAFORK_HOME。
		for _, kv := range extraEnv() {
			env = append(env, kv)
			s.logs.add("system", "Environment: "+kv)
		}
	}

	run := &runState{
		binary:    binary,
		version:   version,
		token:     token,
		startedAt: time.Now(),
		done:      make(chan struct{}),
	}
	onLine := func(stream string) procx.LineHandler {
		return func(line string) {
			run.observe(line)
			s.logs.add(stream, line)
		}
	}

	s.mu.Lock()
	s.run = run
	s.state = StateStarting
	s.lastErr = ""
	s.openWhenReady = openOnReady
	s.mu.Unlock()
	s.logs.add("system", fmt.Sprintf("Starting %s %s", filepath.Base(binary), strings.Join(args, " ")))
	if proxyNote != "" {
		s.logs.add("system", proxyNote)
	}
	s.emitStatus()

	home, _ := os.UserHomeDir()
	proc, err := procx.StartCtx(context.Background(), procx.Spec{
		Name:            binary,
		Args:            args,
		Dir:             home,
		Env:             env,
		CaptureOutput:   true,
		OnStdout:        onLine("stdout"),
		OnStderr:        onLine("stderr"),
		KillGracePeriod: 3 * time.Second,
	})
	if err != nil {
		s.mu.Lock()
		s.run = nil
		s.mu.Unlock()
		return fmt.Errorf("start NarraFork: %w", err)
	}
	run.pid = proc.PID()
	s.mu.Lock()
	s.proc = proc
	s.mu.Unlock()
	s.log.Info("NarraFork started", "pid", run.pid, "binary", binary)
	s.emitStatus()

	go s.waitExit(proc, run)
	go s.waitReady(run)
	return nil
}

func (s *Service) waitExit(proc *procx.Process, run *runState) {
	err := proc.Wait()
	run.mu.Lock()
	run.exitErr = err
	stopping := run.stopping
	lockPID := run.lockPID
	run.mu.Unlock()
	close(run.done)

	s.mu.Lock()
	if s.proc == proc {
		s.proc = nil
	}
	s.mu.Unlock()

	switch {
	case stopping || err == nil:
		s.logs.add("system", "NarraFork stopped")
		s.setState(StateStopped, "")
	case lockPID > 0:
		msg := fmt.Sprintf("another NarraFork process (pid %d) is already using the data directory", lockPID)
		s.logs.add("system", msg)
		s.setState(StateError, msg)
	default:
		msg := "NarraFork exited: " + err.Error()
		s.logs.add("system", msg)
		s.setState(StateError, msg)
	}
}

// readyProbeAfter：`running on` 之后健康检查连续失败这么久，就改在候选回环地址里找真正在应答的那个。
const readyProbeAfter = 10 * time.Second

// probeCandidates 是兜底探测的回环主机；localhost 在前，它在 IPv4 / IPv6 优先的机器上都能用。
var probeCandidates = []string{"localhost", "127.0.0.1", "::1"}

// waitReady 等 `running on` 出现且 /api/health 是 NarraFork 的应答。
// 不设总期限：大库迁移可能要好几分钟，进程退出才算失败。
func (s *Service) waitReady(run *runState) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var listeningSince, lastProbe time.Time
	for {
		select {
		case <-run.done:
			return
		case <-ticker.C:
		}
		if _, _, _, listening := run.address(); !listening {
			continue
		}
		if listeningSince.IsZero() {
			listeningSince = time.Now()
		}
		if !healthy(run.healthURL()) {
			if time.Since(listeningSince) < readyProbeAfter || time.Since(lastProbe) < 5*time.Second {
				continue
			}
			lastProbe = time.Now()
			host, ok := probeLoopback(run)
			if !ok {
				continue
			}
			run.adopt(host)
			s.logs.add("system", fmt.Sprintf("Health check at %s failed; NarraFork answers on %s, using that address", run.hostPort(), host))
		}
		s.mu.Lock()
		if s.run != run || s.state != StateStarting {
			s.mu.Unlock()
			return
		}
		s.state = StateRunning
		open := s.openWhenReady
		s.openWhenReady = false
		s.mu.Unlock()
		s.logs.add("system", "NarraFork is ready at "+run.browserURL())
		s.emitStatus()
		if open {
			_ = s.Open(context.Background())
		}
		return
	}
}

// hostPort 是日志用的 host:port。
func (r *runState) hostPort() string {
	_, host, port, _ := r.address()
	return net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
}

// probeLoopback 依次探测候选回环主机，返回第一个以 NarraFork 身份应答 /api/health 的。
func probeLoopback(run *runState) (string, bool) {
	protocol, _, port, _ := run.address()
	if port == 0 {
		return "", false
	}
	for _, h := range probeCandidates {
		if healthy(protocol + "://" + net.JoinHostPort(h, strconv.Itoa(port))) {
			return h, true
		}
	}
	return "", false
}

func healthy(base string) bool {
	if base == "" {
		return false
	}
	resp, err := loopbackClient.Get(base + "/api/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var body struct {
		Version      string `json:"version"`
		GitAvailable *bool  `json:"gitAvailable"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return false
	}
	return body.Version != "" && body.GitAvailable != nil
}

// stopProcess 先请求 NarraFork 优雅停机，超时后强杀进程树。阻塞到进程退出。
func (s *Service) stopProcess(timeout time.Duration) {
	s.mu.Lock()
	proc, run := s.proc, s.run
	if proc == nil || run == nil {
		s.mu.Unlock()
		return
	}
	s.state = StateStopping
	s.mu.Unlock()
	run.mu.Lock()
	run.stopping = true
	run.mu.Unlock()
	s.logs.add("system", "Stopping NarraFork…")
	s.emitStatus()

	if requestShutdown(run) {
		select {
		case <-run.done:
			return
		case <-time.After(timeout):
			s.logs.add("system", "Graceful shutdown timed out; killing the process")
		}
	}
	proc.Stop()
	select {
	case <-run.done:
	case <-time.After(10 * time.Second):
		s.log.Warn("NarraFork did not exit after kill", "pid", run.pid)
	}
}

// requestShutdown 用 manager token 请求优雅停机：先发 controlURL，连不上再试其它回环主机
// （token 只发往回环地址）。
func requestShutdown(run *runState) bool {
	base := run.controlURL()
	if base == "" {
		return false
	}
	protocol, _, port, _ := run.address()
	bases := []string{base}
	for _, h := range probeCandidates {
		if b := protocol + "://" + net.JoinHostPort(h, strconv.Itoa(port)); b != base {
			bases = append(bases, b)
		}
	}
	for _, b := range bases {
		accepted, reached := postShutdown(b, run.token)
		if reached {
			return accepted
		}
	}
	return false
}

// postShutdown 返回 (服务接受了停机请求, 请求到达了某个 HTTP 服务)。
func postShutdown(base, token string) (accepted, reached bool) {
	req, err := http.NewRequest(http.MethodPost, base+"/api/system/lifecycle/signal", nil)
	if err != nil {
		return false, false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := loopbackClient.Do(req)
	if err != nil {
		return false, false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusConflict, true
}

// —— 日志缓存 ——

const maxLogLines = 3000

// LogLine 是一行服务输出。Stream 为 "stdout" | "stderr" | "system"（启动器自己的提示）。
type LogLine struct {
	Seq    int64  `json:"seq"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
	Time   int64  `json:"time"`
}

// logBuffer 是环形缓存 + 批量推送（每 100ms 合并一次，避免高频输出淹没前端）。
type logBuffer struct {
	mu      sync.Mutex
	lines   []LogLine
	max     int
	seq     int64
	pending []LogLine
	timer   *time.Timer
	emit    func([]LogLine)
}

func newLogBuffer(max int) *logBuffer { return &logBuffer{max: max} }

func (b *logBuffer) add(stream, text string) {
	if len(text) > 8192 {
		text = text[:8192] + "…"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	line := LogLine{Seq: b.seq, Stream: stream, Text: text, Time: time.Now().UnixMilli()}
	b.lines = append(b.lines, line)
	if len(b.lines) > b.max {
		b.lines = append([]LogLine(nil), b.lines[len(b.lines)-b.max:]...)
	}
	if b.emit == nil {
		return
	}
	b.pending = append(b.pending, line)
	if len(b.pending) > b.max {
		b.pending = b.pending[len(b.pending)-b.max:]
	}
	if b.timer == nil {
		b.timer = time.AfterFunc(100*time.Millisecond, b.flush)
	}
}

func (b *logBuffer) flush() {
	b.mu.Lock()
	batch := b.pending
	b.pending = nil
	b.timer = nil
	emit := b.emit
	b.mu.Unlock()
	if len(batch) > 0 && emit != nil {
		emit(batch)
	}
}

func (b *logBuffer) snapshot() []LogLine {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]LogLine(nil), b.lines...)
}

func (b *logBuffer) clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = nil
}

var errNotRunning = errors.New("NarraFork is not running")

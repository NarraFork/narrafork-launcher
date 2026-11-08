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
	lockPID   int
	stopping  bool
	done      chan struct{}
	exitErr   error
}

func (r *runState) address() (protocol, host string, port int, listening bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.protocol, r.host, r.port, r.listening
}

// browserURL 是给人打开的地址：通配监听换成回环。
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
	return protocol + "://" + net.JoinHostPort(h, strconv.Itoa(port))
}

// controlURL 是本机控制 / 健康检查地址：非回环主机名时 NarraFork 会在 127.0.0.1 同端口开一个控制伴随监听。
func (r *runState) controlURL() string {
	protocol, host, port, _ := r.address()
	if port == 0 {
		return ""
	}
	h := strings.Trim(host, "[]")
	if ip := net.ParseIP(h); ip == nil || !ip.IsLoopback() {
		h = "127.0.0.1"
	}
	return protocol + "://" + net.JoinHostPort(h, strconv.Itoa(port))
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
	r.protocol = m[2]
	r.host = m[3]
	r.port = port
	if m[1] == "running" {
		r.listening = true
	}
}

// loopbackClient 只用于访问本机 NarraFork：它可能启用了自签名证书，所以单独一个跳过校验的 client，
// 且只连回环地址（controlURL 保证）。
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
	env := append(os.Environ(), managedEnv+"=1", managerTokenEnv+"="+token)

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

// waitReady 等 `running on` 出现且 /api/health 是 NarraFork 的应答。
// 不设总期限：大库迁移可能要好几分钟，进程退出才算失败。
func (s *Service) waitReady(run *runState) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-run.done:
			return
		case <-ticker.C:
		}
		if _, _, _, listening := run.address(); !listening {
			continue
		}
		if !healthy(run.controlURL()) {
			continue
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

	if base := run.controlURL(); base != "" && requestShutdown(base, run.token) {
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

func requestShutdown(base, token string) bool {
	req, err := http.NewRequest(http.MethodPost, base+"/api/system/lifecycle/signal", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := loopbackClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusConflict
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

// Package launcher 下载并托管 NarraFork 服务进程。
//
// 职责：
//   - 从 GitHub Release（NarraFork/NarraFork）下载当前平台的二进制，校验 SHA256 后放进
//     <数据目录>/bin/<version>/，installed.json 记录当前版本
//   - 以 `--serve` 启动它（Windows 上不带 --serve 会进入 NarraFork 自带的管理面板），
//     采集 stdout/stderr，按 `running on http(s)://host:port` 得出实际地址并做健康检查
//   - 停止时先经 manager token 调 /api/system/lifecycle/signal 让服务优雅退出，超时再强杀
//   - 在内置窗口或系统浏览器中打开 NarraFork，清除内置窗口的浏览数据
package launcher

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"narrafork-launcher/internal/events"
	"narrafork-launcher/internal/services/preferences"
	"narrafork-launcher/internal/utils/filex"
	"narrafork-launcher/internal/utils/logx"
	"narrafork-launcher/internal/utils/procx"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// State 是服务进程状态。
type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateError    State = "error"
)

// Status 随 launcher:status 事件推送，也可经 GetStatus 拉取。
type Status struct {
	State       State  `json:"state"`
	PID         int    `json:"pid"`
	URL         string `json:"url"`         // 浏览器访问地址（通配地址已换成回环）
	Version     string `json:"version"`     // 正在运行（或将要运行）的版本；自定义路径时为空
	Installed   string `json:"installed"`   // 下载目录中的当前版本
	BinaryPath  string `json:"binaryPath"`  // 实际启动的可执行文件
	StartedAt   int64  `json:"startedAt"`   // unix 毫秒；未运行为 0
	Error       string `json:"error"`       // 最近一次失败原因
	Downloading bool   `json:"downloading"` // 是否有下载进行中
	Preparing   bool   `json:"preparing"`   // 是否在执行"检查更新 → 下载 → 启动"流程
}

// Service 是 Wails 服务。
type Service struct {
	prefs   *preferences.Service
	dataDir string
	log     *slog.Logger

	app        *application.App
	mainWindow application.Window

	mu        sync.Mutex
	state     State
	lastErr   string
	proc      *procx.Process
	run       *runState
	releases  []*ReleaseInfo
	dlCancel  context.CancelFunc
	preparing bool
	logs      *logBuffer
	inline    *application.WebviewWindow
	net       *netManager
	// memConfig 是没有 preferences（单元测试 / E2E）时的内存配置。
	memConfig *Config

	// onConfig 在配置保存后调用（app 层据此同步托盘等）。
	onConfig func(Config)
	// openWhenReady 为 true 时，本轮启动就绪后按配置打开 NarraFork。
	openWhenReady bool
	// extraEnv 返回启动 NarraFork 时追加的环境变量（后者覆盖继承的同名变量）。
	extraEnv func() []string
}

// New 创建服务。dataDir 为启动器数据根目录（下载的二进制放在其下 bin/）。
func New(prefs *preferences.Service, dataDir string) *Service {
	s := &Service{
		prefs:   prefs,
		dataDir: dataDir,
		log:     logx.For("launcher"),
		state:   StateStopped,
		logs:    newLogBuffer(maxLogLines),
	}
	s.net = newNetManager(func(msg string) {
		s.log.Info(msg)
		s.logs.add("system", msg)
	})
	return s
}

// Attach 在 application.New 之后回填 app 与主窗口。写成包级函数，避免被 Wails 生成为前端可调用方法。
func Attach(s *Service, app *application.App, mainWindow application.Window, onConfig func(Config)) {
	s.app = app
	s.mainWindow = mainWindow
	s.onConfig = onConfig
	s.logs.emit = func(lines []LogLine) { app.Event.Emit(events.LauncherLog, lines) }
}

// SetExtraEnv 设置启动 NarraFork 时追加的环境变量来源（如 narrastore 的 NARRAFORK_HOME）。
// 写成包级函数，避免被 Wails 生成为前端方法。
func SetExtraEnv(s *Service, fn func() []string) {
	s.mu.Lock()
	s.extraEnv = fn
	s.mu.Unlock()
}

// ServiceStartup 由 Wails 在应用启动时调用：按配置自动下载 / 更新并启动。
func (s *Service) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	cfg := s.loadConfig(ctx)
	s.net.sync(cfg)
	if cfg.AutoStart {
		go s.prepareAndStart(cfg, cfg.OpenOnReady)
	} else if cfg.AutoUpdate && cfg.BinaryPath == "" {
		go s.backgroundUpdate()
	}
	return nil
}

// ServiceShutdown 在应用退出时停止 NarraFork。
func (s *Service) ServiceShutdown() error {
	s.CancelDownload()
	s.stopProcess(15 * time.Second)
	return nil
}

// GetStatus 返回当前状态快照。
func (s *Service) GetStatus() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *Service) statusLocked() Status {
	st := Status{
		State:       s.state,
		Error:       s.lastErr,
		Downloading: s.dlCancel != nil,
		Preparing:   s.preparing,
	}
	if inst, err := s.readInstalled(); err == nil && inst != nil {
		st.Installed = inst.Version
	}
	if r := s.run; r != nil {
		st.PID = r.pid
		st.Version = r.version
		st.BinaryPath = r.binary
		st.URL = r.browserURL()
		if s.state == StateRunning || s.state == StateStarting {
			st.StartedAt = r.startedAt.UnixMilli()
		}
	}
	return st
}

func (s *Service) emitStatus() {
	if s.app == nil {
		return
	}
	s.app.Event.Emit(events.LauncherStatus, s.GetStatus())
}

func (s *Service) emitProgress(p DownloadProgress) {
	if s.app == nil {
		return
	}
	s.app.Event.Emit(events.LauncherDownload, p)
}

func (s *Service) setState(state State, errMsg string) {
	s.mu.Lock()
	s.state = state
	s.lastErr = errMsg
	s.mu.Unlock()
	s.emitStatus()
}

// GetLogs 返回缓存的最近日志（打开页面时补齐历史）。
func (s *Service) GetLogs() []LogLine { return s.logs.snapshot() }

// ClearLogs 清空日志缓存。
func (s *Service) ClearLogs() { s.logs.clear() }

// DataDir 返回下载目录，供界面展示。
func (s *Service) DataDir() string { return s.binDir() }

// GetNetworkStatus 返回下载源 / 代理的配置与本次会话实际使用的源。
func (s *Service) GetNetworkStatus(ctx context.Context) NetworkStatus {
	return s.net.status(s.loadConfig(ctx))
}

// TestNetwork 测试 GitHub 直连、镜像与当前下载代理的连通性（结果逐项返回，不抛错）。
func (s *Service) TestNetwork(ctx context.Context) []NetworkTestResult {
	cfg := s.loadConfig(ctx)
	res := s.net.test(ctx, cfg)
	parts := make([]string, 0, len(res))
	for _, r := range res {
		switch {
		case r.Skipped:
			parts = append(parts, r.ID+"=skipped")
		case r.OK:
			parts = append(parts, fmt.Sprintf("%s=ok %dms", r.ID, r.LatencyMs))
		default:
			parts = append(parts, fmt.Sprintf("%s=%s", r.ID, r.ErrorKind))
		}
	}
	s.logs.add("system", "Network test: "+strings.Join(parts, ", "))
	return res
}

// updateCheckTimeout：启动前检查更新的总时限（auto 模式可能先等 GitHub 超时再换镜像）。
const updateCheckTimeout = 60 * time.Second

// prepareAndStart：（按需）检查更新 → 下载 → 启动。供自动启动与界面的"启动"按钮共用。
func (s *Service) prepareAndStart(cfg Config, openOnReady bool) {
	s.mu.Lock()
	if s.preparing {
		s.mu.Unlock()
		return
	}
	s.preparing = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.preparing = false
		s.mu.Unlock()
		s.emitStatus()
	}()
	s.emitStatus()

	if cfg.BinaryPath == "" {
		inst, _ := s.readInstalled()
		if inst == nil || cfg.AutoUpdate {
			ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
			check, err := s.CheckUpdate(ctx)
			cancel()
			switch {
			case err != nil && inst == nil:
				s.logs.add("system", "Update check failed: "+err.Error())
				s.setState(StateError, "download: "+err.Error())
				return
			case err != nil:
				s.log.Warn("update check failed; starting installed version", "err", err)
				s.logs.add("system", "Update check failed; starting the installed version: "+err.Error())
			case check.Available && check.Latest != nil:
				if _, err := s.download(check.Latest); err != nil && inst == nil {
					s.setState(StateError, "download: "+err.Error())
					return
				}
			}
		}
	}
	if err := s.start(cfg, openOnReady); err != nil {
		s.setState(StateError, err.Error())
	}
}

// backgroundUpdate 只下载不启动（未开启自动启动但开启了自动更新）。
func (s *Service) backgroundUpdate() {
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	check, err := s.CheckUpdate(ctx)
	cancel()
	if err != nil || !check.Available || check.Latest == nil {
		return
	}
	_, _ = s.download(check.Latest)
}

// Start 启动 NarraFork（未下载时先下载）。立即返回，进度经事件推送。
func (s *Service) Start(ctx context.Context) {
	cfg := s.loadConfig(ctx)
	go s.prepareAndStart(cfg, cfg.OpenOnReady)
}

// Stop 优雅停止 NarraFork。
func (s *Service) Stop() {
	s.stopProcess(30 * time.Second)
}

// Restart 停止后重新启动（会使用下载目录中的最新版本）。
func (s *Service) Restart(ctx context.Context) {
	cfg := s.loadConfig(ctx)
	go func() {
		s.stopProcess(30 * time.Second)
		if err := s.start(cfg, false); err != nil {
			s.setState(StateError, err.Error())
		}
	}()
}

// —— WebView2 数据目录（清除浏览数据用） ——

// WebviewDataDir 返回内置浏览器（WebView2）用户数据目录。
func WebviewDataDir(dataDir string) string { return filepath.Join(dataDir, "webview") }

func (s *Service) wipeMarker() string { return filepath.Join(s.dataDir, "webview.clear") }

// ConsumeWebviewWipe 在创建任何窗口之前调用：上次请求了"彻底清除"时删掉 WebView2 数据目录。
func ConsumeWebviewWipe(dataDir string) {
	marker := filepath.Join(dataDir, "webview.clear")
	if _, err := os.Stat(marker); err != nil {
		return
	}
	if err := os.RemoveAll(WebviewDataDir(dataDir)); err != nil {
		logx.For("launcher").Warn("wipe webview data failed", "err", err)
		return
	}
	_ = os.Remove(marker)
}

func (s *Service) scheduleWebviewWipe() error {
	if err := filex.EnsureDir(s.dataDir); err != nil {
		return err
	}
	return filex.WriteAtomic(s.wipeMarker(), []byte(time.Now().Format(time.RFC3339)), 0o644)
}

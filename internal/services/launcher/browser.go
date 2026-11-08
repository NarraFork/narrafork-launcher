package launcher

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"narrafork-launcher/internal/utils/procx"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const inlineWindowName = "narrafork-app"

// Open 按配置的打开方式打开 NarraFork（inline / external；none 视为 external，因为是用户主动点击）。
func (s *Service) Open(ctx context.Context) error {
	cfg := s.loadConfig(ctx)
	if cfg.OpenMode == "inline" {
		return s.OpenInline()
	}
	return s.OpenExternal()
}

func (s *Service) currentURL() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil || s.state != StateRunning {
		return "", errNotRunning
	}
	return s.run.browserURL(), nil
}

// OpenExternal 在系统默认浏览器中打开。
func (s *Service) OpenExternal() error {
	url, err := s.currentURL()
	if err != nil {
		return err
	}
	return s.app.Browser.OpenURL(url)
}

// OpenInline 在启动器内置窗口中打开（已打开则聚焦）。
func (s *Service) OpenInline() error {
	url, err := s.currentURL()
	if err != nil {
		return err
	}
	s.mu.Lock()
	win := s.inline
	s.mu.Unlock()
	if win != nil {
		application.InvokeSync(func() {
			win.UnMinimise()
			win.Show()
			win.Focus()
		})
		return nil
	}
	win = s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      inlineWindowName,
		Title:     "NarraFork",
		Width:     1280,
		Height:    820,
		MinWidth:  800,
		MinHeight: 500,
		URL:       url,
		// 远程页面不需要 Wails 运行时能力；保留原生标题栏，不用自绘。
		DevToolsEnabled: true,
	})
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		s.mu.Lock()
		if s.inline == win {
			s.inline = nil
		}
		s.mu.Unlock()
	})
	s.mu.Lock()
	s.inline = win
	s.mu.Unlock()
	win.Show()
	return nil
}

func (s *Service) closeInline() {
	s.mu.Lock()
	win := s.inline
	s.inline = nil
	s.mu.Unlock()
	if win != nil {
		win.Close()
	}
}

// clearSiteDataJS 清掉 NarraFork 源下的站点数据：Service Worker、Cache Storage、
// localStorage / sessionStorage、IndexedDB 与非 HttpOnly 的 Cookie。
const clearSiteDataJS = `(async () => {
  try { const regs = await navigator.serviceWorker?.getRegistrations?.() ?? []; await Promise.all(regs.map(r => r.unregister())); } catch {}
  try { const keys = await caches.keys(); await Promise.all(keys.map(k => caches.delete(k))); } catch {}
  try { localStorage.clear(); sessionStorage.clear(); } catch {}
  try { const dbs = await indexedDB.databases?.() ?? []; dbs.forEach(d => d.name && indexedDB.deleteDatabase(d.name)); } catch {}
  try { document.cookie.split(';').forEach(c => { const n = c.split('=')[0].trim(); if (n) document.cookie = n + '=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/'; }); } catch {}
})();`

// ClearBrowsingData 清除内置浏览器中 NarraFork 的站点数据（缓存 / Service Worker / 本地存储 / Cookie），
// 并关闭内置窗口。需要服务正在运行，以便在 NarraFork 源下执行清理。
func (s *Service) ClearBrowsingData() error {
	url, err := s.currentURL()
	if err != nil {
		return err
	}
	s.closeInline()
	win := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "narrafork-clear",
		Title:  "NarraFork",
		Width:  400,
		Height: 300,
		Hidden: true,
		URL:    url,
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
		},
	})
	// ExecJS 在页面运行时就绪后才执行；给清理脚本留足时间再关窗口。
	win.ExecJS(clearSiteDataJS)
	go func() {
		time.Sleep(6 * time.Second)
		win.Close()
	}()
	s.logs.add("system", "Browsing data for "+url+" cleared")
	return nil
}

// ResetWebviewData 安排在下次启动时删除整个内置浏览器数据目录（所有站点、权限、HttpOnly Cookie）。
func (s *Service) ResetWebviewData() error {
	return s.scheduleWebviewWipe()
}

// —— 依赖检测 ——

// Dependency 描述一个外部工具。
type Dependency struct {
	Name       string `json:"name"`
	Required   bool   `json:"required"`
	Installed  bool   `json:"installed"`
	Version    string `json:"version"`
	Path       string `json:"path"`
	InstallURL string `json:"installUrl"`
}

// GitIdentity 是 git 全局用户信息。
type GitIdentity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func checkTool(ctx context.Context, name string, args ...string) (path, version string, ok bool) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", "", false
	}
	out, err := runCapture(ctx, p, args...)
	if err != nil {
		return p, "", true
	}
	first := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	return p, first, true
}

func runCapture(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var b strings.Builder
	err := procx.Run(ctx, procx.Spec{
		Name:          name,
		Args:          args,
		CaptureOutput: true,
		OnStdout: func(line string) {
			if b.Len() < 64<<10 {
				b.WriteString(line)
				b.WriteByte('\n')
			}
		},
	})
	return b.String(), err
}

// CheckDependencies 检测 Git（必需）、ripgrep（推荐）以及 Linux 上的 Podman（可选）。
func (s *Service) CheckDependencies(ctx context.Context) []Dependency {
	deps := []Dependency{
		{Name: "git", Required: true, InstallURL: "https://git-scm.com/downloads"},
		{Name: "rg", Required: false, InstallURL: "https://github.com/BurntSushi/ripgrep/releases"},
	}
	if runtime.GOOS == "linux" {
		deps = append(deps, Dependency{Name: "podman", InstallURL: "https://podman.io/docs/installation"})
	}
	for i := range deps {
		deps[i].Path, deps[i].Version, deps[i].Installed = checkTool(ctx, deps[i].Name, "--version")
	}
	return deps
}

// GetGitIdentity 读取 git 全局 user.name / user.email。
func (s *Service) GetGitIdentity(ctx context.Context) GitIdentity {
	git, err := exec.LookPath("git")
	if err != nil {
		return GitIdentity{}
	}
	name, _ := runCapture(ctx, git, "config", "--global", "user.name")
	email, _ := runCapture(ctx, git, "config", "--global", "user.email")
	return GitIdentity{Name: strings.TrimSpace(name), Email: strings.TrimSpace(email)}
}

// SetGitIdentity 写入 git 全局 user.name / user.email（空值跳过）。
func (s *Service) SetGitIdentity(ctx context.Context, id GitIdentity) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return errors.New("git is not installed")
	}
	for key, val := range map[string]string{"user.name": id.Name, "user.email": id.Email} {
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		if strings.ContainsAny(val, "\r\n") || len(val) > 256 {
			return errors.New("invalid value for " + key)
		}
		if _, err := runCapture(ctx, git, "config", "--global", key, val); err != nil {
			return err
		}
	}
	return nil
}

// OpenURL 在系统浏览器打开依赖安装页 / 发布页。只允许 https，避免被当作任意程序启动器。
func (s *Service) OpenURL(url string) error {
	if !strings.HasPrefix(url, "https://") {
		return errors.New("only https URLs can be opened")
	}
	return s.app.Browser.OpenURL(url)
}

// RevealDownloads 在文件管理器中打开下载目录。
func (s *Service) RevealDownloads() error {
	dir := s.binDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return s.app.Browser.OpenFile(filepath.Clean(dir))
}

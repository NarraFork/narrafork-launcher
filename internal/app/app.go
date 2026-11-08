// Package app 封装 Wails 应用的启动逻辑。
// main.go 只保留 embed.FS 与 Run 调用，业务装配集中在这里。
package app

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"sync/atomic"

	"narrafork-launcher/internal/events"
	"narrafork-launcher/internal/services/appsettings"
	"narrafork-launcher/internal/services/exportsvc"
	"narrafork-launcher/internal/services/launcher"
	"narrafork-launcher/internal/services/narrastore"
	"narrafork-launcher/internal/services/preferences"
	"narrafork-launcher/internal/services/storagesvc"
	"narrafork-launcher/internal/services/subprocess"
	"narrafork-launcher/internal/storage"
	"narrafork-launcher/internal/utils/httpx"
	"narrafork-launcher/internal/utils/logx"

	"github.com/wailsapp/wails/v3/pkg/application"
	wailsevents "github.com/wailsapp/wails/v3/pkg/events"
)

// AppVersion 由构建注入（-ldflags "-X narrafork-launcher/internal/app.AppVersion=..."），默认取开发值。
var AppVersion = "dev"

// Run 创建 Wails 应用、注册服务、打开主窗口并启动事件循环。
// assets 由调用方（main.go）通过 //go:embed 注入。
func Run(assets embed.FS, trayIcon []byte) error {
	// —— 工具层先就绪：日志 → HTTP UA → 后续模块都能直接用 ——
	if err := logx.Init(logx.Config{
		Level:          slog.LevelInfo,
		ConsoleEnabled: true,
	}); err != nil {
		// 日志初始化失败不应阻断启动；降级到默认 stderr logger（logx.For 内部已兜底）
		fmt.Println("warn: logx init failed:", err)
	}
	httpx.SetUserAgent("NarraFork-Launcher/" + AppVersion)

	events.Register()

	// —— 持久化层启动顺序 ——
	// 1) 读 storage.json：用户自定义路径 / 默认路径
	// 2) 用 effectivePath 打开 SQLite（PRAGMA + AutoMigrate + IntegrityCheck）
	// 3) 包成 Holder，业务 service 全部走 holder.Current() 拿活跃 DB
	cfgMgr, err := storage.LoadConfig()
	if err != nil {
		return fmt.Errorf("load storage config: %w", err)
	}
	dbPath, err := cfgMgr.EffectiveDBPath()
	if err != nil {
		return fmt.Errorf("resolve db path: %w", err)
	}
	db, err := storage.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	holder := storage.NewHolder(db)
	logx.For("app").Info("storage opened", "path", dbPath)

	dataDir, err := storage.DataDir()
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	// 上次请求了"重置内置浏览器数据"：在任何 WebView 创建之前删除其数据目录。
	launcher.ConsumeWebviewWipe(dataDir)

	// subprocess service 需要 *application.App 才能向前端 Emit 事件。
	// 但 application.New 又需要 services 列表 —— 用预创建 + Bind 模式：
	//   先 new 一个空 service 占位，然后在 app.New 之后回填 app 引用。
	subSvc := subprocess.New(nil) // app 暂为 nil
	childWinSvc := NewChildWindowService()
	prefsSvc := preferences.New(holder)
	systraySvc := NewSystrayService(prefsSvc, trayIcon)
	launcherSvc := launcher.New(prefsSvc, dataDir)
	// NarraFork 数据目录管理：删除前需确认 NarraFork 未运行（starting / running / stopping 都算运行中）。
	narraStoreSvc := narrastore.New(prefsSvc, func() bool {
		switch launcherSvc.GetStatus().State {
		case launcher.StateStarting, launcher.StateRunning, launcher.StateStopping:
			return true
		}
		return false
	})
	// 自定义 NarraFork 数据目录时，启动 NarraFork 带上 NARRAFORK_HOME。
	launcher.SetExtraEnv(launcherSvc, func() []string { return narrastore.ChildEnv(narraStoreSvc) })

	var mainWindow *application.WebviewWindow
	app := application.New(application.Options{
		Name:        "NarraFork Launcher",
		Description: "Downloads, updates and runs NarraFork",
		Services: []application.Service{
			application.NewService(prefsSvc),
			application.NewService(appsettings.New(holder)),
			application.NewService(storagesvc.New(holder, cfgMgr)),
			application.NewService(exportsvc.New()),
			application.NewService(subSvc),
			application.NewService(childWinSvc),
			application.NewService(systraySvc),
			application.NewService(launcherSvc),
			application.NewService(narraStoreSvc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.narrafork.launcher",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if mainWindow != nil {
					mainWindow.UnMinimise()
					mainWindow.Show()
					mainWindow.Focus()
				}
			},
		},
		Windows: application.WindowsOptions{
			// 内置浏览器数据放在启动器数据目录下，"重置内置浏览器数据"直接删该目录。
			WebviewUserDataPath: launcher.WebviewDataDir(dataDir),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})
	subSvc.AttachApp(app)
	childWinSvc.AttachApp(app)
	narrastore.Attach(narraStoreSvc, app)

	mainWindow = app.Window.NewWithOptions(windowOptions())

	// 关闭主窗口：按配置最小化到托盘或退出。退出时 launcher.ServiceShutdown 负责停止 NarraFork。
	var quitting atomic.Bool
	app.OnShutdown(func() { quitting.Store(true) })
	closeAction := atomic.Value{}
	closeAction.Store(launcherSvc.GetConfig(context.Background()).CloseAction)
	mainWindow.RegisterHook(wailsevents.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting.Load() {
			return
		}
		if closeAction.Load() == "minimize" {
			e.Cancel()
			// 托盘未开时先打开，否则窗口隐藏后没有入口找回。
			if !systraySvc.IsEnabled() {
				_ = systraySvc.Enable(context.Background())
			}
			mainWindow.Hide()
			return
		}
		e.Cancel()
		go app.Quit()
	})

	launcher.Attach(launcherSvc, app, mainWindow, func(cfg launcher.Config) {
		closeAction.Store(cfg.CloseAction)
		if cfg.CloseAction == "minimize" && !systraySvc.IsEnabled() {
			_ = systraySvc.Enable(context.Background())
		}
	})

	// 系统托盘：开关持久化在 preferences 表，这里按上次会话的状态恢复；
	// 关闭行为是"最小化到托盘"时总是启用托盘。
	systraySvc.attach(app, mainWindow)
	systraySvc.restore(context.Background())
	if closeAction.Load() == "minimize" && !systraySvc.IsEnabled() {
		_ = systraySvc.Enable(context.Background())
	}

	defer logx.Close()
	return app.Run()
}

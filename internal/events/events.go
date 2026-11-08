package events

import "github.com/wailsapp/wails/v3/pkg/application"

// Names 集中声明前后端共享的事件名，避免硬编码字符串散落各处。
const (
	Time = "time"
	// AppSettingsChanged 在 app_settings 任一字段写入成功后广播（所有窗口都能收到）。
	// payload 为变更的字段名："themeChoice" | "customTheme" | "localeChoice"。
	// 托盘面板 / 子窗口据此重新拉取设置，实现主题、语言跨窗口实时同步。
	AppSettingsChanged = "appsettings:changed"

	// LauncherStatus 在 NarraFork 服务状态变化时广播，payload 为 launcher.Status。
	LauncherStatus = "launcher:status"
	// LauncherLog 批量推送服务输出，payload 为 []launcher.LogLine。
	LauncherLog = "launcher:log"
	// LauncherDownload 推送下载进度，payload 为 launcher.DownloadProgress。
	LauncherDownload = "launcher:download"
)

// Register 在 application 初始化前注册所有强类型事件，
// Wails 的 binding 生成器会据此为前端生成类型化 API。
// launcher:* 的 payload 是 launcher 包里的结构体；为避免 events → services 的反向依赖，这里按 any 注册。
func Register() {
	application.RegisterEvent[string](Time)
	application.RegisterEvent[string](AppSettingsChanged)
	application.RegisterEvent[any](LauncherStatus)
	application.RegisterEvent[any](LauncherLog)
	application.RegisterEvent[any](LauncherDownload)
}

//go:build !windows

package proxyx

// readRegistry：非 Windows 平台没有注册表来源，系统代理只来自环境变量。
func readRegistry() Settings { return Settings{} }

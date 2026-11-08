//go:build windows

package proxyx

import "golang.org/x/sys/windows/registry"

const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// readRegistry 读取当前用户的 WinINet 代理设置（"设置 → 网络和 Internet → 代理"的手动代理）。
// 自动配置脚本（AutoConfigURL / PAC）不解析。
func readRegistry() Settings {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return Settings{}
	}
	defer k.Close()
	enable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enable == 0 {
		return Settings{}
	}
	server, _, _ := k.GetStringValue("ProxyServer")
	override, _, _ := k.GetStringValue("ProxyOverride")
	return ParseWinINet(uint32(enable), server, override)
}

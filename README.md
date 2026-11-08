# NarraFork Launcher

NarraFork 的外置启动器与下载器。它从 [NarraFork/NarraFork](https://github.com/NarraFork/NarraFork/releases) 的 GitHub Release 下载当前平台的 NarraFork 二进制，校验后启动，并在桌面窗口里显示服务状态和输出。

基于 [FxRayHughes/foundation](https://github.com/FxRayHughes/foundation)（Wails v3 + React 19 + Mantine 9）构建，界面布局参考 [narrafork-launcher-gui](https://github.com/NarraFork/narrafork-launcher-gui)。

## 功能

| 页面 | 内容 |
|------|------|
| 控制台 | 服务状态、运行时长、访问地址；启动 / 停止 / 重启 / 打开；服务输出终端（ANSI 彩色与 JSON 日志，3000 行缓存，向上滚动暂停跟随） |
| 版本 | 已下载版本与当前通道最新版本；下载进度（速度、取消）；可下载版本列表，可安装任意版本（含回退） |
| 依赖 | 检测 Git（必需）、ripgrep（推荐）、Podman（仅 Linux，可选）；设置 git 全局 user.name / user.email |
| 设置 → 启动器 | 端口 / 主机、自定义可执行文件、自动启动、就绪后自动打开、打开方式（内置窗口 / 系统浏览器）、关闭行为（最小化到托盘 / 退出）、自动更新、更新通道（稳定版 / 测试版）、网络（下载源 / 镜像地址、下载代理、NarraFork 代理、网络测试）、清除内置浏览器数据 |
| 设置 → 其它 | 脚手架自带的主题、布局、语言、数据存储 |
| 托盘面板 | 服务状态与启动 / 停止 / 打开 |

## 工作方式

**下载**

- 稳定版通道取 `GET /repos/NarraFork/NarraFork/releases/latest`，测试版通道取最近 30 个发布中最新的一个（含预发布）。只认 `vX.Y.Z` 形式的 tag，`executor-v*` 之类的辅助发布被跳过。发布信息在内存中缓存 5 分钟。
- 资产名为 `narrafork-<version>-<os>-<arch>[.exe]`（`windows-x64.exe`、`macos-arm64`、`linux-x64` 等）。校验优先用 API 资产上的 `digest`（`sha256:<hex>`），没有时取同一发布的 `narrafork-<version>-SHA256SUMS`（同一下载源）；两者都拿不到时拒绝安装。不匹配的文件丢弃。
- 下载先写 `<文件>.part`：建连 / TLS 超时 15 秒，等待响应头 30 秒，60 秒收不到数据视为停滞。网络层失败时最多重试 3 次，用 `Range` 从已下载处续传（要求 `206` 且 `Content-Range` 与请求一致，否则从头下载）。
- 文件放在 `<数据目录>/bin/<version>/`，`bin/installed.json` 记录当前版本、下载源与校验依据。下载新版本后保留当前和上一个版本，其余删除。
- 开启自动更新时，每次启动前检查一次；检查失败但已有版本时直接启动已有版本。
- 匿名访问 GitHub API 每小时限 60 次（镜像有自己的同等限额）。设置环境变量 `GITHUB_TOKEN` 可提高 GitHub 直连的限额；令牌只发给 `api.github.com`，不会发给镜像。

**下载源与代理**（设置 → 启动器 → 网络）

- 下载源 `downloadSource`：`auto`（默认）先直连 GitHub（建连超时 8 秒），遇到网络层失败（DNS、超时、拒绝、重置、TLS、代理）时改用镜像，并在本次会话内记住；HTTP 4xx 等不会触发切换。`github` 只直连，`mirror` 只走镜像。API、校验文件、二进制都按同一规则。修改下载源或代理后重新从 GitHub 开始尝试。
- 镜像 `mirrorUrl` 默认 `https://hk1.aeoliancloud.com/`（HubProxy），请求地址为「镜像地址 + 原始完整 URL」，例如 `https://hk1.aeoliancloud.com/https://api.github.com/repos/NarraFork/NarraFork/releases/latest`。镜像返回的 JSON 中 `browser_download_url` 仍是 github.com，由启动器改写。
- 实际使用的源显示在版本页、下载进度和控制台日志中。
- 下载代理 `downloadProxyMode`：`system`（默认）读 `HTTPS_PROXY` / `HTTP_PROXY` / `ALL_PROXY`（含小写，`NO_PROXY` 作为绕过列表），没有时在 Windows 上读注册表 `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`（`ProxyEnable=1` 时的 `ProxyServer` 与 `ProxyOverride`，含 `<local>`；不解析 PAC）；`direct` 直连；`custom` 用 `downloadProxyUrl`（`http` / `https` / `socks5` / `socks5h`，带密码时加密保存）。回环地址始终直连。
- NarraFork 代理 `narraforkProxyMode` 决定写给 NarraFork 进程的 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY`（及小写）与 `NO_PROXY`（`localhost,127.0.0.1,::1` 加绕过列表）：`inherit`（默认，原样继承启动器的环境）、`none`（移除代理变量）、`system`、`custom`、`download`（与下载代理相同）。NarraFork 默认直连，只在启动时读取这些变量，且只有它的「设置 → 代理」选「跟随系统」时自己的出站请求才会使用；git、终端等子进程直接继承。下次启动或重启 NarraFork 时生效。
- 网络测试：分别测试 GitHub API 与 github.com 直连、镜像、经下载代理访问 github.com，显示延迟或错误类型。

**启动与停止**

- 以 `--serve` 启动 NarraFork。Windows 版不带该参数会进入 NarraFork 自带的管理面板。
- 设置了端口 / 主机时追加 `--port=` / `--host=`；留空则沿用 NarraFork 自己的 `~/.narrafork/settings.json`。
- 进程环境带 `NARRAFORK_MANAGER_MANAGED=1`（服务不再自行打开浏览器）和一次性的 `NARRAFORK_MANAGER_TOKEN`。
- 实际地址取服务输出中的 `running on http(s)://host:port`，再轮询 `/api/health` 确认是 NarraFork 在应答。`localhost` 保持原样交给系统解析（可能只解析到 `::1`），通配地址 `0.0.0.0` / `::` 换成 `127.0.0.1` / `::1`。看到 `running on` 后 10 秒仍连不上时，依次探测 `localhost`、`127.0.0.1`、`::1`，改用第一个以 NarraFork 身份应答的地址（控制台记一行日志）。启动没有总超时（大库迁移可能要几分钟），进程退出才算失败。
- 停止时用 manager token 调 `POST /api/system/lifecycle/signal`（只发往回环地址：回环主机名原样，其它主机名 / 局域网地址改发 NarraFork 在 `127.0.0.1` 同端口开的控制伴随监听），等服务自行退出（30 秒）；超时或请求失败时终止整个进程树。子进程绑在 Windows JobObject / Unix 进程组上，启动器崩溃时 NarraFork 会一起退出。
- 退出启动器时会停止 NarraFork。

**内置浏览器**

- 内置窗口使用启动器自己的 WebView2 数据目录 `<数据目录>/webview`。
- “清除站点数据”在 NarraFork 源下注销 Service Worker、清空 Cache Storage、localStorage、sessionStorage、IndexedDB 和非 HttpOnly Cookie，需要服务在运行。
- “重置内置浏览器”在下次启动启动器时删除整个 `webview` 目录（含 HttpOnly Cookie 和权限）。

数据目录：Windows `%AppData%\NarraForkLauncher`，macOS `~/Library/Application Support/NarraForkLauncher`，Linux `~/.local/share/NarraForkLauncher`。环境变量 `NARRAFORK_LAUNCHER_DATA_DIR` 可覆盖（便携版）。

## 开发

前置：Go ≥ 1.26、Node.js ≥ 20、pnpm、`wails3` CLI（`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0`）。

| 命令 | 作用 |
|------|------|
| `cd frontend && pnpm install` | 安装前端依赖 |
| `wails3 generate bindings` | 生成前端绑定（改了 Go service 后必须重跑） |
| `wails3 dev` | 热重载开发 |
| `wails3 task windows:build` | 生产构建，产物 `bin/narrafork-launcher.exe`（macOS / Linux 用 `darwin:build` / `linux:build`） |
| `wails3 task package` | 打包安装程序 |
| `cd frontend && pnpm typecheck` | 前端类型检查 |
| `go test ./internal/...` | 后端单元测试 |
| `LAUNCHER_LIVE=1 go test ./internal/services/launcher -run Live -v` | 只读访问真实 GitHub Release 与镜像（含 digest、SHA256SUMS、一次小的 Range 请求、网络测试） |
| `LAUNCHER_E2E=1 go test ./internal/services/launcher -run EndToEnd -v` | 下载真实稳定版（约 190 MB）、启动、优雅停止；NarraFork 的 home 目录重定向到临时目录。`LAUNCHER_E2E_SOURCE=mirror` 强制走镜像 |

## 代码结构

```
internal/services/launcher/   下载器与进程托管（本项目的业务核心）
  service.go    状态、事件、自动启动 / 更新流程
  release.go    GitHub Release 查询、下载（续传 / 停滞检测 / 重试）、SHA-256 校验、版本清理
  network.go    下载源（auto 回退 / 镜像）、按代理缓存的 HTTP client、发布信息缓存、错误分类、网络测试、子进程代理环境
  process.go    启动 / 健康检查（回环探测兜底）/ 优雅停止、日志缓存
  browser.go    内置窗口 / 系统浏览器、清除浏览数据、依赖检测、git 身份
  config.go     启动器配置（存 SQLite preferences 表的 launcher.config；代理密码经 cryptox 加密）
internal/utils/proxyx/        系统代理（环境变量 / Windows 注册表）、代理地址校验、绕过规则、子进程代理变量
internal/app/app.go           服务注册、单实例、关闭到托盘、WebView2 数据目录
frontend/src/services/launcher/  前端服务封装
frontend/src/pages/ConsolePage    控制台
frontend/src/pages/VersionsPage   版本
frontend/src/pages/DependenciesPage  依赖
frontend/src/pages/SettingsPage/launcher  设置 → 启动器
```

脚手架的工程规范（MVVM、i18n、持久化、工具层约束等）见 [CLAUDE.md](./CLAUDE.md) 与 [frontend/CLAUDE.md](./frontend/CLAUDE.md)。

## License

MIT

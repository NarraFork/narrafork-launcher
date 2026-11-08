package launcher

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"narrafork-launcher/internal/utils/filex"
	"narrafork-launcher/internal/utils/httpx"
)

// Repository 是 NarraFork 二进制发布所在的 GitHub 仓库。
const Repository = "NarraFork/NarraFork"

const (
	apiBase          = "https://api.github.com/repos/" + Repository
	manifestFileName = "installed.json"
	// keepVersions 下载新版本后保留的版本数（含当前），其余目录删除。
	keepVersions = 2
	// downloadTimeout 单次下载的总时长上限（约 190MB）。
	downloadTimeout = 2 * time.Hour
)

// 只认 NarraFork 主程序的 tag（v0.9.12），跳过 executor-v0.9.5 之类的辅助发布。
var appTag = regexp.MustCompile(`^v\d+\.\d+\.\d+([-.][0-9A-Za-z.]+)?$`)

// ReleaseInfo 是给前端展示的一个可下载版本。
type ReleaseInfo struct {
	Version     string `json:"version"`
	Tag         string `json:"tag"`
	Name        string `json:"name"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"publishedAt"`
	Notes       string `json:"notes"`
	HTMLURL     string `json:"htmlUrl"`
	AssetName   string `json:"assetName"`
	AssetSize   int64  `json:"assetSize"`
	// assetURL / sumsURL 只在后端使用，不暴露给前端（下载必须走 Install）。
	assetURL string
	sumsURL  string
}

// Installed 记录下载目录中当前使用的版本。
type Installed struct {
	Version     string `json:"version"`
	File        string `json:"file"`
	Channel     string `json:"channel"`
	InstalledAt string `json:"installedAt"`
}

// UpdateCheck 是检查更新的结果。
type UpdateCheck struct {
	Current   string       `json:"current"`
	Latest    *ReleaseInfo `json:"latest"`
	Available bool         `json:"available"`
	Platform  string       `json:"platform"`
}

// DownloadProgress 随 launcher:download 事件推送。
type DownloadProgress struct {
	Version     string `json:"version"`
	Received    int64  `json:"received"`
	Total       int64  `json:"total"`
	Phase       string `json:"phase"` // "downloading" | "verifying" | "done" | "error" | "cancelled"
	Error       string `json:"error"`
	BytesPerSec int64  `json:"bytesPerSec"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt string    `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// platformSuffix 返回当前平台的资产后缀，如 "windows-x64.exe" / "linux-arm64"。
func platformSuffix() (string, error) {
	var osName string
	switch runtime.GOOS {
	case "windows":
		osName = "windows"
	case "darwin":
		osName = "macos"
	case "linux":
		osName = "linux"
	default:
		return "", fmt.Errorf("unsupported OS %s", runtime.GOOS)
	}
	var arch string
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("unsupported architecture %s", runtime.GOARCH)
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return osName + "-" + arch + ext, nil
}

func githubGet[T any](ctx context.Context, url string, out *T) error {
	req, cancel, err := httpx.NewRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	defer cancel()
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := httpx.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("GitHub API rate limit reached (HTTP %d); try again later or set GITHUB_TOKEN", resp.StatusCode)
		}
		return fmt.Errorf("GitHub API %s: HTTP %d", url, resp.StatusCode)
	}
	return httpx.ReadJSONLimit(resp, 16<<20, out)
}

// toReleaseInfo 选出本平台的资产；没有对应资产返回 nil。
func toReleaseInfo(r ghRelease) *ReleaseInfo {
	if r.Draft || !appTag.MatchString(r.TagName) {
		return nil
	}
	suffix, err := platformSuffix()
	if err != nil {
		return nil
	}
	version := strings.TrimPrefix(r.TagName, "v")
	want := "narrafork-" + version + "-" + suffix
	info := &ReleaseInfo{
		Version:     version,
		Tag:         r.TagName,
		Name:        r.Name,
		Prerelease:  r.Prerelease,
		PublishedAt: r.PublishedAt,
		Notes:       r.Body,
		HTMLURL:     r.HTMLURL,
	}
	for _, a := range r.Assets {
		switch a.Name {
		case want:
			info.AssetName = a.Name
			info.AssetSize = a.Size
			info.assetURL = a.BrowserDownloadURL
		case "narrafork-" + version + "-SHA256SUMS":
			info.sumsURL = a.BrowserDownloadURL
		}
	}
	if info.assetURL == "" {
		return nil
	}
	return info
}

// fetchReleases 拉最近的发布列表（含预发布），按发布时间倒序。
func (s *Service) fetchReleases(ctx context.Context) ([]*ReleaseInfo, error) {
	var raw []ghRelease
	if err := githubGet(ctx, apiBase+"/releases?per_page=30", &raw); err != nil {
		return nil, err
	}
	out := make([]*ReleaseInfo, 0, len(raw))
	for _, r := range raw {
		if info := toReleaseInfo(r); info != nil {
			out = append(out, info)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].PublishedAt > out[j].PublishedAt })
	s.mu.Lock()
	s.releases = out
	s.mu.Unlock()
	return out, nil
}

// latestFor 返回通道内最新版本：stable 走 /releases/latest，beta 取列表中最新的一项（含预发布）。
func (s *Service) latestFor(ctx context.Context, channel string) (*ReleaseInfo, error) {
	if channel == "beta" {
		list, err := s.fetchReleases(ctx)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, errors.New("no release has a binary for this platform")
		}
		return list[0], nil
	}
	var r ghRelease
	if err := githubGet(ctx, apiBase+"/releases/latest", &r); err != nil {
		return nil, err
	}
	info := toReleaseInfo(r)
	if info == nil {
		return nil, fmt.Errorf("release %s has no binary for this platform", r.TagName)
	}
	return info, nil
}

// ListReleases 返回最近的可下载版本（含预发布）。
func (s *Service) ListReleases(ctx context.Context) ([]*ReleaseInfo, error) {
	return s.fetchReleases(ctx)
}

// CheckUpdate 按配置的通道检查最新版本。
func (s *Service) CheckUpdate(ctx context.Context) (UpdateCheck, error) {
	suffix, _ := platformSuffix()
	cfg := s.loadConfig(ctx)
	res := UpdateCheck{Platform: suffix}
	if inst, err := s.readInstalled(); err == nil && inst != nil {
		res.Current = inst.Version
	}
	latest, err := s.latestFor(ctx, cfg.Channel)
	if err != nil {
		return res, err
	}
	res.Latest = latest
	res.Available = res.Current == "" || compareVersions(latest.Version, res.Current) > 0
	return res, nil
}

// compareVersions 比较 x.y.z（忽略预发布后缀之外的差异）；a>b 返回 1。
func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] > pb[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func versionParts(v string) [3]int {
	var out [3]int
	core := strings.SplitN(strings.TrimPrefix(v, "v"), "-", 2)[0]
	for i, p := range strings.SplitN(core, ".", 3) {
		if i > 2 {
			break
		}
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}

// —— 已安装版本 ——

func (s *Service) binDir() string { return filepath.Join(s.dataDir, "bin") }

func (s *Service) readInstalled() (*Installed, error) {
	buf, err := filex.ReadLimit(filepath.Join(s.binDir(), manifestFileName), 64<<10)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var inst Installed
	if err := json.Unmarshal(buf, &inst); err != nil {
		return nil, err
	}
	if inst.Version == "" || inst.File == "" {
		return nil, nil
	}
	return &inst, nil
}

func (s *Service) installedPath(inst *Installed) string {
	return filepath.Join(s.binDir(), inst.Version, filepath.Base(inst.File))
}

// resolveBinary 返回要启动的可执行文件：配置的自定义路径优先，否则用下载目录中的当前版本。
func (s *Service) resolveBinary(cfg Config) (path, version string, err error) {
	if cfg.BinaryPath != "" {
		if st, err := os.Stat(cfg.BinaryPath); err != nil || st.IsDir() {
			return "", "", fmt.Errorf("custom binary not found: %s", cfg.BinaryPath)
		}
		return cfg.BinaryPath, "", nil
	}
	inst, err := s.readInstalled()
	if err != nil {
		return "", "", err
	}
	if inst == nil {
		return "", "", errNotInstalled
	}
	p := s.installedPath(inst)
	if _, err := os.Stat(p); err != nil {
		return "", "", errNotInstalled
	}
	return p, inst.Version, nil
}

var errNotInstalled = errors.New("NarraFork is not downloaded yet")

// —— 下载 ——

// Install 下载指定版本（空串 = 当前通道最新版）并设为当前版本。下载期间进度经 launcher:download 推送。
// 服务运行中也可以下载，新版本在下次启动（或 Restart）时生效。
func (s *Service) Install(ctx context.Context, version string) (Installed, error) {
	var info *ReleaseInfo
	var err error
	if version == "" {
		info, err = s.latestFor(ctx, s.loadConfig(ctx).Channel)
	} else {
		info, err = s.findRelease(ctx, version)
	}
	if err != nil {
		return Installed{}, err
	}
	return s.download(info)
}

func (s *Service) findRelease(ctx context.Context, version string) (*ReleaseInfo, error) {
	version = strings.TrimPrefix(version, "v")
	s.mu.Lock()
	cached := s.releases
	s.mu.Unlock()
	for _, list := range [][]*ReleaseInfo{cached, nil} {
		if list == nil {
			var err error
			if list, err = s.fetchReleases(ctx); err != nil {
				return nil, err
			}
		}
		for _, r := range list {
			if r.Version == version {
				return r, nil
			}
		}
	}
	return nil, fmt.Errorf("version %s not found for this platform", version)
}

// CancelDownload 取消进行中的下载。
func (s *Service) CancelDownload() {
	s.mu.Lock()
	cancel := s.dlCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// IsDownloading 返回是否有下载进行中。
func (s *Service) IsDownloading() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dlCancel != nil
}

func (s *Service) download(info *ReleaseInfo) (Installed, error) {
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	s.mu.Lock()
	if s.dlCancel != nil {
		s.mu.Unlock()
		cancel()
		return Installed{}, errors.New("a download is already in progress")
	}
	s.dlCancel = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.dlCancel = nil
		s.mu.Unlock()
	}()

	inst, err := s.downloadTo(ctx, info)
	if err != nil {
		phase := "error"
		if errors.Is(ctx.Err(), context.Canceled) {
			phase = "cancelled"
		}
		s.emitProgress(DownloadProgress{Version: info.Version, Phase: phase, Error: err.Error()})
		return Installed{}, err
	}
	s.emitProgress(DownloadProgress{Version: info.Version, Phase: "done", Received: info.AssetSize, Total: info.AssetSize})
	s.log.Info("NarraFork downloaded", "version", inst.Version)
	s.emitStatus()
	return inst, nil
}

func (s *Service) downloadTo(ctx context.Context, info *ReleaseInfo) (Installed, error) {
	expected := ""
	if info.sumsURL != "" {
		sum, err := fetchChecksum(ctx, info.sumsURL, info.AssetName)
		if err != nil {
			return Installed{}, fmt.Errorf("read checksums: %w", err)
		}
		expected = sum
	}

	dir := filepath.Join(s.binDir(), info.Version)
	if err := filex.EnsureDir(dir); err != nil {
		return Installed{}, err
	}
	final := filepath.Join(dir, info.AssetName)
	part := final + ".part"

	req, reqCancel, err := httpx.NewRequest(ctx, http.MethodGet, info.assetURL, nil)
	if err != nil {
		return Installed{}, err
	}
	defer reqCancel()
	resp, err := httpx.Do(req)
	if err != nil {
		return Installed{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Installed{}, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	total := resp.ContentLength
	if total <= 0 {
		total = info.AssetSize
	}

	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return Installed{}, err
	}
	hash := sha256.New()
	pw := &progressWriter{s: s, version: info.Version, total: total, start: time.Now()}
	_, copyErr := io.Copy(io.MultiWriter(f, hash, pw), resp.Body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(part)
		if copyErr != nil {
			return Installed{}, copyErr
		}
		return Installed{}, closeErr
	}
	if total > 0 && pw.received != total {
		_ = os.Remove(part)
		return Installed{}, fmt.Errorf("incomplete download: %d of %d bytes", pw.received, total)
	}

	s.emitProgress(DownloadProgress{Version: info.Version, Phase: "verifying", Received: pw.received, Total: total})
	if expected != "" {
		if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, expected) {
			_ = os.Remove(part)
			return Installed{}, fmt.Errorf("checksum mismatch for %s", info.AssetName)
		}
	}
	_ = os.Remove(final)
	if err := os.Rename(part, final); err != nil {
		return Installed{}, err
	}
	if err := os.Chmod(final, 0o755); err != nil {
		return Installed{}, err
	}

	inst := Installed{
		Version:     info.Version,
		File:        info.AssetName,
		Channel:     map[bool]string{true: "beta", false: "stable"}[info.Prerelease],
		InstalledAt: time.Now().Format(time.RFC3339),
	}
	buf, _ := json.MarshalIndent(inst, "", "  ")
	if err := filex.WriteAtomic(filepath.Join(s.binDir(), manifestFileName), buf, 0o644); err != nil {
		return Installed{}, err
	}
	s.pruneVersions(inst.Version)
	return inst, nil
}

// fetchChecksum 从 SHA256SUMS（`<hex>  <name>` 每行一条）里取出指定文件的摘要。
func fetchChecksum(ctx context.Context, url, name string) (string, error) {
	req, cancel, err := httpx.NewRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	defer cancel()
	resp, err := httpx.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum listed for %s", name)
}

// pruneVersions 只保留最近 keepVersions 个版本目录（当前版本总是保留；正在运行的文件删不掉时跳过）。
func (s *Service) pruneVersions(current string) {
	entries, err := os.ReadDir(s.binDir())
	if err != nil {
		return
	}
	running := ""
	s.mu.Lock()
	if s.run != nil {
		running = s.run.version
	}
	s.mu.Unlock()
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != current && e.Name() != running && appTag.MatchString("v"+e.Name()) {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return compareVersions(dirs[i], dirs[j]) > 0 })
	for i, d := range dirs {
		if i < keepVersions-1 {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.binDir(), d)); err != nil {
			s.log.Warn("remove old version failed", "version", d, "err", err)
		}
	}
}

type progressWriter struct {
	s        *Service
	version  string
	total    int64
	received int64
	start    time.Time
	lastEmit time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.received += int64(len(b))
	now := time.Now()
	if now.Sub(p.lastEmit) >= 200*time.Millisecond {
		p.lastEmit = now
		var bps int64
		if el := now.Sub(p.start).Seconds(); el > 0 {
			bps = int64(float64(p.received) / el)
		}
		p.s.emitProgress(DownloadProgress{
			Version: p.version, Received: p.received, Total: p.total, Phase: "downloading", BytesPerSec: bps,
		})
	}
	return len(b), nil
}

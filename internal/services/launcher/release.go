package launcher

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
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
	// downloadAttempts：每个源最多尝试次数（首次 + 2 次续传重试）。
	downloadAttempts = 3
	// downloadHeaderTimeout：发出下载请求后等待响应头的上限。
	downloadHeaderTimeout = 30 * time.Second
)

// 测试可调小。
var (
	// stallTimeout：下载中这么久没收到任何字节就放弃本次尝试（随后续传重试）。
	stallTimeout = 60 * time.Second
	// retryBackoff：第 n 次重试前等待 n × retryBackoff。
	retryBackoff = 2 * time.Second
)

// 只认 NarraFork 主程序的 tag（v0.9.12），跳过 executor-v0.9.5 之类的辅助发布。
var appTag = regexp.MustCompile(`^v\d+\.\d+\.\d+([-.][0-9A-Za-z.]+)?$`)

var sha256Hex = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

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
	// assetURL / sumsURL 是 GitHub 原始地址（镜像 JSON 里也不改写），由下载源决定实际请求地址。
	// digest 是 API 资产上的 "sha256:<hex>"。只在后端使用（下载必须走 Install）。
	assetURL string
	sumsURL  string
	digest   string
}

// Installed 记录下载目录中当前使用的版本。
type Installed struct {
	Version     string `json:"version"`
	File        string `json:"file"`
	Channel     string `json:"channel"`
	InstalledAt string `json:"installedAt"`
	Source      string `json:"source,omitempty"`   // 下载时使用的源："github" | "mirror"
	Checksum    string `json:"checksum,omitempty"` // 校验依据："digest" | "sums"
}

// UpdateCheck 是检查更新的结果。
type UpdateCheck struct {
	Current   string       `json:"current"`
	Latest    *ReleaseInfo `json:"latest"`
	Available bool         `json:"available"`
	Platform  string       `json:"platform"`
	Source    string       `json:"source"` // 发布信息来自哪个源："github" | "mirror"
	Cached    bool         `json:"cached"` // 是否命中 5 分钟缓存
}

// DownloadProgress 随 launcher:download 事件推送。
type DownloadProgress struct {
	Version     string `json:"version"`
	Received    int64  `json:"received"`
	Total       int64  `json:"total"`
	Phase       string `json:"phase"` // "downloading" | "retrying" | "verifying" | "done" | "error" | "cancelled"
	Error       string `json:"error"`
	BytesPerSec int64  `json:"bytesPerSec"`
	// Source / Host 是当前尝试使用的源与主机（如 "mirror" / "hk1.aeoliancloud.com"）。
	Source string `json:"source"`
	Host   string `json:"host"`
	// Attempt 从 1 开始；Resumed 表示本次尝试从已下载的部分续传。
	Attempt int  `json:"attempt"`
	Resumed bool `json:"resumed"`
	// ErrorKind 是 NetError.Kind（phase 为 error / retrying 时），供界面翻译。
	ErrorKind string `json:"errorKind"`
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
	Digest             string `json:"digest"` // "sha256:<hex>"，较新的 GitHub API 才有
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
			info.digest = a.Digest
		case "narrafork-" + version + "-SHA256SUMS":
			info.sumsURL = a.BrowserDownloadURL
		}
	}
	if info.assetURL == "" {
		return nil
	}
	return info
}

// digestHex 取出 "sha256:<hex>" 中的摘要；格式不对返回空串。
func digestHex(d string) string {
	algo, hexSum, ok := strings.Cut(strings.TrimSpace(d), ":")
	if !ok || !strings.EqualFold(algo, "sha256") || !sha256Hex.MatchString(hexSum) {
		return ""
	}
	return strings.ToLower(hexSum)
}

// fetchReleases 拉最近的发布列表（含预发布），按发布时间倒序。
func (s *Service) fetchReleases(ctx context.Context) ([]*ReleaseInfo, string, bool, error) {
	var raw []ghRelease
	src, cached, err := s.net.getAPI(ctx, s.loadConfig(ctx), apiBase+"/releases?per_page=30", &raw)
	if err != nil {
		return nil, src, false, err
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
	return out, src, cached, nil
}

// latestFor 返回通道内最新版本：stable 走 /releases/latest，beta 取列表中最新的一项（含预发布）。
// 同时返回发布信息来自哪个源、是否命中缓存。
func (s *Service) latestFor(ctx context.Context, channel string) (*ReleaseInfo, string, bool, error) {
	if channel == "beta" {
		list, src, cached, err := s.fetchReleases(ctx)
		if err != nil {
			return nil, src, false, err
		}
		if len(list) == 0 {
			return nil, src, cached, errors.New("no release has a binary for this platform")
		}
		return list[0], src, cached, nil
	}
	var r ghRelease
	src, cached, err := s.net.getAPI(ctx, s.loadConfig(ctx), apiBase+"/releases/latest", &r)
	if err != nil {
		return nil, src, false, err
	}
	info := toReleaseInfo(r)
	if info == nil {
		return nil, src, cached, fmt.Errorf("release %s has no binary for this platform", r.TagName)
	}
	return info, src, cached, nil
}

// ListReleases 返回最近的可下载版本（含预发布）。
func (s *Service) ListReleases(ctx context.Context) ([]*ReleaseInfo, error) {
	list, _, _, err := s.fetchReleases(ctx)
	return list, err
}

// CheckUpdate 按配置的通道检查最新版本。
func (s *Service) CheckUpdate(ctx context.Context) (UpdateCheck, error) {
	suffix, _ := platformSuffix()
	cfg := s.loadConfig(ctx)
	res := UpdateCheck{Platform: suffix}
	if inst, err := s.readInstalled(); err == nil && inst != nil {
		res.Current = inst.Version
	}
	latest, src, cached, err := s.latestFor(ctx, cfg.Channel)
	res.Source, res.Cached = src, cached
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
		info, _, _, err = s.latestFor(ctx, s.loadConfig(ctx).Channel)
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
			if list, _, _, err = s.fetchReleases(ctx); err != nil {
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
		p := DownloadProgress{Version: info.Version, Phase: "error", Error: err.Error(), ErrorKind: errKind(err)}
		var ne *NetError
		if errors.As(err, &ne) {
			p.Source, p.Host = ne.Source, ne.Host
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			p.Phase, p.ErrorKind = "cancelled", "cancelled"
		}
		s.emitProgress(p)
		s.logs.add("system", "Download failed: "+err.Error())
		return Installed{}, err
	}
	s.emitProgress(DownloadProgress{
		Version: info.Version, Phase: "done", Received: info.AssetSize, Total: info.AssetSize,
		Source: inst.Source, Host: s.sourceHost(inst.Source),
	})
	s.log.Info("NarraFork downloaded", "version", inst.Version, "source", inst.Source, "checksum", inst.Checksum)
	s.logs.add("system", fmt.Sprintf("Downloaded NarraFork v%s from %s (SHA-256 verified via %s)", inst.Version, describeSource(s.loadConfig(ctx), inst.Source), inst.Checksum))
	s.emitStatus()
	return inst, nil
}

func (s *Service) sourceHost(src string) string {
	if src == srcMirror {
		return hostOf(s.loadConfig(context.Background()).MirrorURL)
	}
	return "github.com"
}

func (s *Service) downloadTo(ctx context.Context, info *ReleaseInfo) (Installed, error) {
	cfg := s.loadConfig(ctx)
	expected, basis, err := s.expectedChecksum(ctx, cfg, info)
	if err != nil {
		return Installed{}, err
	}

	dir := filepath.Join(s.binDir(), info.Version)
	if err := filex.EnsureDir(dir); err != nil {
		return Installed{}, err
	}
	final := filepath.Join(dir, info.AssetName)
	part := final + ".part"

	var sum string
	sources := len(s.net.order(cfg))
	src, err := s.net.do(ctx, cfg, func(ctx context.Context, src string) error {
		var err error
		sum, err = s.fetchBinary(ctx, cfg, src, info, part, sources > 1)
		return err
	})
	if err != nil {
		return Installed{}, err
	}

	s.emitProgress(DownloadProgress{Version: info.Version, Phase: "verifying", Received: info.AssetSize, Total: info.AssetSize, Source: src, Host: s.sourceHost(src)})
	if !strings.EqualFold(sum, expected) {
		_ = os.Remove(part)
		return Installed{}, &NetError{
			Kind: "checksum", Op: opDownload, Source: src, Host: s.sourceHost(src),
			Message: fmt.Sprintf("checksum mismatch for %s (expected %s, got %s); the file was discarded", info.AssetName, expected, sum),
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
		Source:      src,
		Checksum:    basis,
	}
	buf, _ := json.MarshalIndent(inst, "", "  ")
	if err := filex.WriteAtomic(filepath.Join(s.binDir(), manifestFileName), buf, 0o644); err != nil {
		return Installed{}, err
	}
	s.pruneVersions(inst.Version)
	return inst, nil
}

// expectedChecksum 取期望的 SHA-256：优先 API 资产上的 digest，其次同源的 SHA256SUMS。
// 两者都拿不到时失败——不安装未经校验的文件。
func (s *Service) expectedChecksum(ctx context.Context, cfg Config, info *ReleaseInfo) (sum, basis string, err error) {
	if d := digestHex(info.digest); d != "" {
		return d, "digest", nil
	}
	if info.sumsURL == "" {
		return "", "", &NetError{
			Kind: "checksum", Op: opChecksum,
			Message: fmt.Sprintf("release v%s publishes no SHA-256 digest or SHA256SUMS for %s; refusing to install an unverified file", info.Version, info.AssetName),
		}
	}
	_, err = s.net.do(ctx, cfg, func(ctx context.Context, src string) error {
		body, err := s.net.fetch(ctx, cfg, src, opChecksum, info.sumsURL, 1<<20)
		if err != nil {
			return err
		}
		v, perr := parseChecksums(body, info.AssetName)
		if perr != nil {
			return &NetError{Kind: "checksum", Op: opChecksum, Source: src, Host: hostOf(mirrored(cfg, src, info.sumsURL)), Message: perr.Error()}
		}
		sum = v
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return sum, "sums", nil
}

// parseChecksums 从 SHA256SUMS（`<hex>  <name>` 每行一条）里取出指定文件的摘要。
func parseChecksums(body []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && sha256Hex.MatchString(fields[0]) {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS lists no checksum for %s; refusing to install an unverified file", name)
}

// fetchBinary 在一个源上下载到 part，最多 downloadAttempts 次：网络层失败 / 停滞后用 Range 从已下载处续传。
// canSwitch 为 true（auto 模式还有别的源）时，连接都建不起来的失败直接交给上层换源，不在本源重试。
// 返回整个文件的 SHA-256。
func (s *Service) fetchBinary(ctx context.Context, cfg Config, src string, info *ReleaseInfo, part string, canSwitch bool) (string, error) {
	target := mirrored(cfg, src, info.assetURL)
	host := hostOf(target)
	c, err := s.net.client(cfg, clientKey{dial: dialFor(cfg, src), header: downloadHeaderTimeout})
	if err != nil {
		return "", err
	}
	var last error
	for attempt := 1; attempt <= downloadAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt-1) * retryBackoff):
			}
		}
		sum, err := s.downloadAttempt(ctx, c, src, host, target, info, part, attempt)
		if err == nil {
			return sum, nil
		}
		last = err
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		kind := errKind(err)
		if !fallbackKind(kind) {
			return "", err
		}
		var ne *NetError
		if canSwitch && errors.As(err, &ne) && ne.preResponse {
			return "", err
		}
		if attempt < downloadAttempts {
			st, _ := os.Stat(part)
			var have int64
			if st != nil {
				have = st.Size()
			}
			s.logs.add("system", fmt.Sprintf("%s; retrying (%d/%d) from byte %d", err.Error(), attempt+1, downloadAttempts, have))
			s.emitProgress(DownloadProgress{
				Version: info.Version, Phase: "retrying", Received: have, Total: info.AssetSize,
				Source: src, Host: host, Attempt: attempt + 1, Error: err.Error(), ErrorKind: kind,
			})
		}
	}
	return "", last
}

// downloadAttempt 发一次下载请求。part 已有数据时带 Range 续传；服务端必须回 206 且 Content-Range
// 与请求一致，否则截断从头下载。返回整个文件的 SHA-256。
func (s *Service) downloadAttempt(ctx context.Context, c *http.Client, src, host, target string, info *ReleaseInfo, part string, attempt int) (string, error) {
	var offset int64
	if st, err := os.Stat(part); err == nil {
		offset = st.Size()
		if info.AssetSize > 0 && offset > info.AssetSize {
			offset = 0
		}
	}
	if info.AssetSize > 0 && offset == info.AssetSize {
		// 上次已下载完整（例如校验前被中断）：直接算哈希，不再请求。
		h := sha256.New()
		if err := hashPrefix(part, offset, h); err == nil {
			return hex.EncodeToString(h.Sum(nil)), nil
		}
		offset = 0
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", httpx.UserAgent())
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := httpx.DoWith(c, req)
	if err != nil {
		ne := newNetError(opDownload, src, target, err)
		ne.preResponse = true
		return "", ne
	}
	defer resp.Body.Close()

	total := info.AssetSize
	resumed := false
	switch {
	case offset > 0 && resp.StatusCode == http.StatusPartialContent:
		start, size, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != offset || (info.AssetSize > 0 && size != info.AssetSize) {
			// 区间对不上：不信任，从头下载（下一次尝试）。
			_ = os.Remove(part)
			return "", &NetError{Kind: "network", Op: opDownload, Source: src, Host: host,
				Message: fmt.Sprintf("download via %s: server returned an unexpected range %q; restarting from the beginning", sourceLabel(src), resp.Header.Get("Content-Range"))}
		}
		resumed = true
		if total <= 0 {
			total = size
		}
	case resp.StatusCode == http.StatusOK:
		offset = 0 // 服务端忽略了 Range，整个文件重来
		if total <= 0 {
			total = resp.ContentLength
		}
	case offset > 0 && resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(part)
		return "", &NetError{Kind: "network", Op: opDownload, Source: src, Host: host,
			Message: fmt.Sprintf("download via %s: cannot resume (HTTP 416); restarting from the beginning", sourceLabel(src))}
	default:
		return "", httpStatusError(opDownload, src, target, resp)
	}

	flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if resumed {
		flag = os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(part, flag, 0o755)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if resumed {
		if err := hashPrefix(part, offset, h); err != nil {
			_ = f.Close()
			_ = os.Remove(part)
			return "", err
		}
	}

	pw := &progressWriter{
		s: s, version: info.Version, total: total, received: offset, start: time.Now(), base: offset,
		source: src, host: host, attempt: attempt, resumed: resumed,
	}
	s.emitProgress(pw.snapshot())
	// 停滞检测：每收到数据就重置计时器，stallTimeout 内没有数据则取消本次请求。
	stall := time.AfterFunc(stallTimeout, func() { cancel(errStalled) })
	defer stall.Stop()
	body := &stallReader{r: resp.Body, timer: stall}
	_, copyErr := io.Copy(io.MultiWriter(f, h, pw), body)
	closeErr := f.Close()
	if copyErr != nil {
		if cause := context.Cause(ctx); errors.Is(cause, errStalled) {
			copyErr = fmt.Errorf("%w (%s without data)", errStalled, stallTimeout)
		}
		return "", newNetError(opDownload, src, target, copyErr)
	}
	if closeErr != nil {
		return "", closeErr
	}
	if total > 0 && pw.received != total {
		return "", &NetError{Kind: "reset", Op: opDownload, Source: src, Host: host,
			Message: fmt.Sprintf("download via %s ended early: %d of %d bytes", sourceLabel(src), pw.received, total)}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// parseContentRange 解析 "bytes <start>-<end>/<size>"。
func parseContentRange(v string) (start, size int64, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(v), "bytes ")
	if !found {
		return 0, 0, false
	}
	rng, total, found := strings.Cut(rest, "/")
	if !found {
		return 0, 0, false
	}
	a, b, found := strings.Cut(rng, "-")
	if !found {
		return 0, 0, false
	}
	start, err1 := strconv.ParseInt(a, 10, 64)
	end, err2 := strconv.ParseInt(b, 10, 64)
	size, err3 := strconv.ParseInt(total, 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || start < 0 || end < start || end >= size {
		return 0, 0, false
	}
	return start, size, true
}

// hashPrefix 把 part 的前 n 字节喂给 h（续传时补齐整文件哈希）。
func hashPrefix(path string, n int64, h hash.Hash) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.CopyN(h, f, n); err != nil {
		return fmt.Errorf("read partial download: %w", err)
	}
	return nil
}

type stallReader struct {
	r     io.Reader
	timer *time.Timer
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.timer.Reset(stallTimeout)
	}
	return n, err
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
	base     int64 // 本次尝试开始时已有的字节（续传），速度只按本次新收到的算
	start    time.Time
	lastEmit time.Time
	source   string
	host     string
	attempt  int
	resumed  bool
}

func (p *progressWriter) snapshot() DownloadProgress {
	var bps int64
	if el := time.Since(p.start).Seconds(); el > 0 {
		bps = int64(float64(p.received-p.base) / el)
	}
	return DownloadProgress{
		Version: p.version, Received: p.received, Total: p.total, Phase: "downloading", BytesPerSec: bps,
		Source: p.source, Host: p.host, Attempt: p.attempt, Resumed: p.resumed,
	}
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.received += int64(len(b))
	now := time.Now()
	if now.Sub(p.lastEmit) >= 200*time.Millisecond {
		p.lastEmit = now
		p.s.emitProgress(p.snapshot())
	}
	return len(b), nil
}

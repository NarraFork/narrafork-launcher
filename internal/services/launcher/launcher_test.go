package launcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestObserveAddress(t *testing.T) {
	r := &runState{}
	r.observe(`NarraFork startup page on http://127.0.0.1:7779`)
	if _, _, port, listening := r.address(); port != 7779 || listening {
		t.Fatalf("startup page: port=%d listening=%v", port, listening)
	}
	r.observe(`{"msg":"NarraFork server running on http://0.0.0.0:7780","level":"info"}`)
	proto, host, port, listening := r.address()
	if proto != "http" || host != "0.0.0.0" || port != 7780 || !listening {
		t.Fatalf("running: %s %s %d %v", proto, host, port, listening)
	}
	if got := r.browserURL(); got != "http://127.0.0.1:7780" {
		t.Fatalf("browserURL = %s", got)
	}
	r.observe(`NarraFork server running on https://[::1]:8443`)
	if got := r.browserURL(); got != "https://[::1]:8443" {
		t.Fatalf("ipv6 browserURL = %s", got)
	}
	r.observe(`NarraFork server running on http://nf.lan:9000`)
	if got := r.controlURL(); got != "http://127.0.0.1:9000" {
		t.Fatalf("controlURL for hostname = %s", got)
	}
	if got := r.healthURL(); got != "http://nf.lan:9000" {
		t.Fatalf("healthURL for hostname = %s", got)
	}
	r.observe(`Another NarraFork process (pid 4242) is already using ~/.narrafork`)
	if r.lockPID != 4242 {
		t.Fatalf("lockPID = %d", r.lockPID)
	}
}

func TestControlHost(t *testing.T) {
	cases := map[string]string{
		"localhost":       "localhost",
		"LocalHost":       "localhost",
		"app.localhost":   "app.localhost",
		"0.0.0.0":         "127.0.0.1",
		"":                "127.0.0.1",
		"::":              "::1",
		"[::]":            "::1",
		"::1":             "::1",
		"[::1]":           "::1",
		"127.0.0.1":       "127.0.0.1",
		"127.0.0.2":       "127.0.0.2",
		"192.168.1.20":    "127.0.0.1",
		"nf.lan":          "127.0.0.1",
		"fe80::1":         "127.0.0.1",
		"localhost.evil.": "127.0.0.1",
	}
	for in, want := range cases {
		if got := controlHost(in); got != want {
			t.Errorf("controlHost(%q) = %q, want %q", in, got, want)
		}
	}
	r := &runState{}
	r.observe(`NarraFork server running on http://localhost:7778 ... listenHost=localhost`)
	if got := r.controlURL(); got != "http://localhost:7778" {
		t.Fatalf("controlURL = %s", got)
	}
	if got := r.healthURL(); got != "http://localhost:7778" {
		t.Fatalf("healthURL = %s", got)
	}
	r.adopt("::1")
	if got := r.controlURL(); got != "http://[::1]:7778" {
		t.Fatalf("adopted controlURL = %s", got)
	}
	if got := r.browserURL(); got != "http://[::1]:7778" {
		t.Fatalf("adopted browserURL = %s", got)
	}
	// 地址变化（重新监听）后丢弃探测结果。
	r.observe(`NarraFork server running on http://localhost:7790`)
	if got := r.controlURL(); got != "http://localhost:7790" {
		t.Fatalf("controlURL after re-listen = %s", got)
	}
}

// fakeNarraFork 模拟 NarraFork：/api/health 与带 token 的优雅停机接口。
func fakeNarraFork(t *testing.T, token string, shutdown chan<- struct{}) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"ok","version":"0.9.8+test","gitAvailable":true}`)
		case "/api/system/lifecycle/signal":
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			select {
			case shutdown <- struct{}{}:
			default:
			}
		default:
			http.NotFound(w, r)
		}
	})
}

// listenOnly 在指定回环地址上开 httptest server；该协议栈不可用时跳过。
func listenOnly(t *testing.T, addr string, h http.Handler) *httptest.Server {
	t.Helper()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot listen on %s: %v", addr, err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// TestReadyOnIPv6OnlyLocalhost 复现用户的问题：NarraFork 输出 `running on http://localhost:<port>`，
// 但 localhost 解析到 ::1 且服务只听在 [::1]。健康检查与停机都必须连得上。
func TestReadyOnIPv6OnlyLocalhost(t *testing.T) {
	shutdown := make(chan struct{}, 1)
	srv := listenOnly(t, "[::1]:0", fakeNarraFork(t, "tok", shutdown))
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	if c, err := net.DialTimeout("tcp", net.JoinHostPort("localhost", strconv.Itoa(port)), time.Second); err != nil {
		t.Skipf("localhost does not resolve to ::1 here: %v", err)
	} else {
		_ = c.Close()
	}

	r := &runState{token: "tok", done: make(chan struct{})}
	r.observe(fmt.Sprintf("NarraFork server running on http://localhost:%d ... listenHost=localhost", port))
	if !healthy(r.healthURL()) {
		t.Fatalf("health check at %s failed", r.healthURL())
	}

	s := New(nil, t.TempDir())
	s.run = r
	s.state = StateStarting
	go s.waitReady(r)
	deadline := time.Now().Add(5 * time.Second)
	for s.GetStatus().State != StateRunning {
		if time.Now().After(deadline) {
			t.Fatalf("state = %s, want running", s.GetStatus().State)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !requestShutdown(r) {
		t.Fatal("graceful shutdown request was not accepted")
	}
	select {
	case <-shutdown:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown request did not reach the server")
	}
	close(r.done)
}

// TestReadyProbeAdoptsLoopback：输出的主机连不上（例如 localhost 只解析到一个服务没听的协议栈）时，
// 10 秒后探测候选回环地址并改用应答的那个。
func TestReadyProbeAdoptsLoopback(t *testing.T) {
	srv := listenOnly(t, "127.0.0.1:0", fakeNarraFork(t, "tok", nil))
	port := srv.Listener.Addr().(*net.TCPAddr).Port

	r := &runState{token: "tok", done: make(chan struct{})}
	defer close(r.done)
	// 模拟输出的主机不可达：用一个保留的不可路由名字。
	r.observe(fmt.Sprintf("NarraFork server running on http://unreachable.invalid:%d", port))
	if got, ok := probeLoopback(r); !ok || got != "localhost" && got != "127.0.0.1" {
		t.Fatalf("probeLoopback = %q %v", got, ok)
	}
	if testing.Short() {
		return
	}
	s := New(nil, t.TempDir())
	s.run = r
	s.state = StateStarting
	go s.waitReady(r)
	deadline := time.Now().Add(readyProbeAfter + 10*time.Second)
	for s.GetStatus().State != StateRunning {
		if time.Now().After(deadline) {
			t.Fatalf("state = %s, want running", s.GetStatus().State)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if r.adoptedHost() == "" {
		t.Fatal("expected an adopted host")
	}
	found := false
	for _, l := range s.GetLogs() {
		if l.Stream == "system" && strings.Contains(l.Text, "NarraFork answers on") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a system log line about the adopted address")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.9.12", "0.9.8", 1},
		{"0.9.8", "0.9.12", -1},
		{"v1.0.0", "1.0.0", 0},
		{"1.2.0-beta.1", "1.1.9", 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compare(%s,%s)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestToReleaseInfoPicksPlatformAsset(t *testing.T) {
	suffix, err := platformSuffix()
	if err != nil {
		t.Skip(err)
	}
	r := ghRelease{
		TagName: "v0.9.12",
		Assets: []ghAsset{
			{Name: "narrafork-0.9.12-" + suffix + ".from-0.9.11.zstd-patch", BrowserDownloadURL: "x"},
			{Name: "narrafork-0.9.12-" + strings.Replace(suffix, "x64", "x64-baseline", 1), BrowserDownloadURL: "baseline"},
			{Name: "narrafork-0.9.12-" + suffix, Size: 10, BrowserDownloadURL: "main"},
			{Name: "narrafork-0.9.12-SHA256SUMS", BrowserDownloadURL: "sums"},
		},
	}
	info := toReleaseInfo(r)
	if info == nil || info.assetURL != "main" || info.sumsURL != "sums" || info.Version != "0.9.12" {
		t.Fatalf("unexpected %+v", info)
	}
	if toReleaseInfo(ghRelease{TagName: "executor-v0.9.5", Assets: r.Assets}) != nil {
		t.Fatal("executor release must be ignored")
	}
}

func TestConfigNormalize(t *testing.T) {
	c, err := Config{Channel: "nightly", OpenMode: "x", CloseAction: "y"}.normalize()
	if err != nil || c.Channel != "stable" || c.OpenMode != "inline" || c.CloseAction != "minimize" {
		t.Fatalf("normalize defaults: %+v %v", c, err)
	}
	if c.DownloadSource != "auto" || c.MirrorURL != DefaultMirrorURL || c.DownloadProxyMode != "system" || c.NarraforkProxyMode != "inherit" {
		t.Fatalf("network defaults: %+v", c)
	}
	if _, err := (Config{Host: "a b;rm"}).normalize(); err == nil {
		t.Fatal("expected invalid host")
	}
	if _, err := (Config{Port: 70000}).normalize(); err == nil {
		t.Fatal("expected invalid port")
	}
	if _, err := (Config{Host: "0.0.0.0", Port: 7779}).normalize(); err != nil {
		t.Fatal(err)
	}
	if c, err := (Config{MirrorURL: " https://mirror.example/gh "}).normalize(); err != nil || c.MirrorURL != "https://mirror.example/gh/" {
		t.Fatalf("mirror normalize: %q %v", c.MirrorURL, err)
	}
	for _, bad := range []string{"ftp://x/", "hk1.aeoliancloud.com", "https://user:pw@x/", "https:///nohost"} {
		_, err := (Config{MirrorURL: bad}).normalize()
		var ce *ConfigError
		if !errors.As(err, &ce) || ce.Kind != "invalidMirrorUrl" {
			t.Errorf("mirror %q: err = %v", bad, err)
		}
	}
	if c, err := (Config{DownloadProxyMode: "custom", DownloadProxyURL: "127.0.0.1:7890"}).normalize(); err != nil || c.DownloadProxyURL != "http://127.0.0.1:7890" {
		t.Fatalf("proxy normalize: %q %v", c.DownloadProxyURL, err)
	}
	for _, bad := range []string{"ftp://h:1", "socks4://h:1", "http://h:99999", "http://h:1/path"} {
		_, err := (Config{NarraforkProxyURL: bad}).normalize()
		var ce *ConfigError
		if !errors.As(err, &ce) || ce.Kind != "invalidProxyUrl" || ce.Field != "narraforkProxyUrl" {
			t.Errorf("proxy %q: err = %v", bad, err)
		}
	}
}

func TestSealSecret(t *testing.T) {
	t.Setenv("NARRAFORK_LAUNCHER_DATA_DIR", t.TempDir())
	s := New(nil, t.TempDir())
	for _, plain := range []string{"", "http://127.0.0.1:7890", "socks5://user@h:1"} {
		got, err := sealSecret(plain)
		if err != nil || got != plain {
			t.Fatalf("seal(%q) = %q %v; values without a password stay plain", plain, got, err)
		}
	}
	sealed, err := sealSecret("http://user:secret@proxy:8080")
	if err != nil || !strings.HasPrefix(sealed, sealedPrefix) || strings.Contains(sealed, "secret") {
		t.Fatalf("sealed = %q %v", sealed, err)
	}
	if got := s.openSecret(sealed); got != "http://user:secret@proxy:8080" {
		t.Fatalf("open = %q", got)
	}
}

// testService 返回一个无持久化、不走代理的服务，下载源 / 镜像按参数设置。
func testService(t *testing.T, source, mirror string) *Service {
	t.Helper()
	s := New(nil, t.TempDir())
	cfg := defaultConfig()
	cfg.DownloadSource = source
	cfg.DownloadProxyMode = "direct"
	if mirror != "" {
		cfg.MirrorURL = mirror
	}
	if _, err := s.SaveConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	return s
}

// deadURL 返回一个没有监听者的本机地址（连接被拒绝）。
func deadURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return "http://" + addr
}

func fastRetries(t *testing.T) {
	t.Helper()
	oldStall, oldBackoff := stallTimeout, retryBackoff
	stallTimeout, retryBackoff = 400*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { stallTimeout, retryBackoff = oldStall, oldBackoff })
}

func TestDownloadVerifiesChecksum(t *testing.T) {
	payload := []byte(strings.Repeat("narrafork", 1000))
	sum := sha256.Sum256(payload)
	good := hex.EncodeToString(sum[:])
	name := "narrafork-0.9.12-test"

	for _, tc := range []struct {
		label  string
		digest string
		ok     bool
	}{{"match", good, true}, {"mismatch", strings.Repeat("0", 64), false}} {
		t.Run(tc.label, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
					fmt.Fprintf(w, "%s  other\n%s  %s\n", strings.Repeat("a", 64), tc.digest, name)
					return
				}
				_, _ = w.Write(payload)
			}))
			defer srv.Close()

			s := testService(t, "github", "")
			info := &ReleaseInfo{Version: "0.9.12", AssetName: name, AssetSize: int64(len(payload)),
				assetURL: srv.URL + "/bin", sumsURL: srv.URL + "/SHA256SUMS"}
			inst, err := s.downloadTo(context.Background(), info)
			if !tc.ok {
				var ne *NetError
				if !errors.As(err, &ne) || ne.Kind != "checksum" {
					t.Fatalf("expected checksum error, got %v", err)
				}
				for _, p := range []string{name, name + ".part"} {
					if _, statErr := os.Stat(filepath.Join(s.binDir(), "0.9.12", p)); statErr == nil {
						t.Fatalf("bad download must not be kept (%s)", p)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if inst.Checksum != "sums" || inst.Source != "github" {
				t.Fatalf("inst = %+v", inst)
			}
			got, err := s.readInstalled()
			if err != nil || got == nil || got.Version != inst.Version {
				t.Fatalf("manifest: %+v %v", got, err)
			}
			path, version, err := s.resolveBinary(defaultConfig())
			if err != nil || version != "0.9.12" || filepath.Base(path) != name {
				t.Fatalf("resolveBinary: %s %s %v", path, version, err)
			}
		})
	}
}

func TestDownloadPrefersDigest(t *testing.T) {
	payload := []byte(strings.Repeat("digest", 500))
	sum := sha256.Sum256(payload)
	var sumsHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
			sumsHits++
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	s := testService(t, "github", "")
	info := &ReleaseInfo{Version: "0.9.12", AssetName: "nf", AssetSize: int64(len(payload)),
		assetURL: srv.URL + "/bin", sumsURL: srv.URL + "/SHA256SUMS", digest: "sha256:" + hex.EncodeToString(sum[:])}
	inst, err := s.downloadTo(context.Background(), info)
	if err != nil || inst.Checksum != "digest" || sumsHits != 0 {
		t.Fatalf("inst=%+v err=%v sumsHits=%d", inst, err, sumsHits)
	}

	info.Version, info.digest = "0.9.13", "sha256:"+strings.Repeat("1", 64)
	if _, err := s.downloadTo(context.Background(), info); err == nil || errKind(err) != "checksum" {
		t.Fatalf("digest mismatch: %v", err)
	}
}

func TestDownloadFailsClosedWithoutChecksum(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	s := testService(t, "github", "")
	info := &ReleaseInfo{Version: "0.9.12", AssetName: "nf", AssetSize: 10, assetURL: srv.URL + "/bin"}
	_, err := s.downloadTo(context.Background(), info)
	if errKind(err) != "checksum" || hits != 0 {
		t.Fatalf("err=%v hits=%d; must not download without a checksum", err, hits)
	}
	// SHA256SUMS 不列出该文件：同样拒绝。
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  other-file\n", strings.Repeat("a", 64))
	}))
	defer srv2.Close()
	info.sumsURL = srv2.URL + "/SHA256SUMS"
	if _, err := s.downloadTo(context.Background(), info); errKind(err) != "checksum" {
		t.Fatalf("unlisted file: %v", err)
	}
	if inst, _ := s.readInstalled(); inst != nil {
		t.Fatal("nothing must be installed")
	}
}

// rangeServer 提供 payload：第一次请求只发前 cut 字节就断开（或停住 stall），之后正常支持 Range。
type rangeServer struct {
	payload []byte
	cut     int
	stall   bool
	badCR   bool // 第一次续传时返回错误的 Content-Range
	ranges  []string
}

func (rs *rangeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rs.ranges = append(rs.ranges, r.Header.Get("Range"))
	n := len(rs.ranges)
	if n == 1 {
		w.Header().Set("Content-Length", strconv.Itoa(len(rs.payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(rs.payload[:rs.cut])
		w.(http.Flusher).Flush()
		if rs.stall {
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	if rs.badCR && n == 2 {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(rs.payload)-1, len(rs.payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(rs.payload)
		return
	}
	http.ServeContent(w, r, "bin", time.Time{}, strings.NewReader(string(rs.payload)))
}

func TestDownloadResumes(t *testing.T) {
	payload := make([]byte, 256<<10)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	cut := 100 << 10

	for _, tc := range []struct {
		name       string
		rs         *rangeServer
		wantRanges []string
	}{
		{"reset", &rangeServer{cut: cut}, []string{"", fmt.Sprintf("bytes=%d-", cut)}},
		{"stall", &rangeServer{cut: cut, stall: true}, []string{"", fmt.Sprintf("bytes=%d-", cut)}},
		{"badContentRange", &rangeServer{cut: cut, badCR: true}, []string{"", fmt.Sprintf("bytes=%d-", cut), ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fastRetries(t)
			tc.rs.payload = payload
			srv := httptest.NewServer(tc.rs)
			defer srv.Close()
			s := testService(t, "github", "")
			info := &ReleaseInfo{Version: "0.9.12", AssetName: "nf", AssetSize: int64(len(payload)), assetURL: srv.URL + "/bin", digest: digest}
			if _, err := s.downloadTo(context.Background(), info); err != nil {
				dumpLogs(t, s)
				t.Fatal(err)
			}
			if fmt.Sprint(tc.rs.ranges) != fmt.Sprint(tc.wantRanges) {
				t.Fatalf("ranges = %q, want %q", tc.rs.ranges, tc.wantRanges)
			}
			got, err := os.ReadFile(filepath.Join(s.binDir(), "0.9.12", "nf"))
			if err != nil || string(got) != string(payload) {
				t.Fatalf("file mismatch (%d bytes) %v", len(got), err)
			}
		})
	}
}

func TestDownloadGivesUpAfterRetries(t *testing.T) {
	fastRetries(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()
	s := testService(t, "github", "")
	info := &ReleaseInfo{Version: "0.9.12", AssetName: "nf", AssetSize: 10, assetURL: srv.URL + "/bin", digest: "sha256:" + strings.Repeat("0", 64)}
	_, err := s.downloadTo(context.Background(), info)
	if err == nil || hits != downloadAttempts || !fallbackKind(errKind(err)) {
		t.Fatalf("err=%v kind=%s hits=%d", err, errKind(err), hits)
	}
}

// fakeGitHubMirror 是一个"镜像"：把 /<原始 URL> 映射到假的 API / 资产。
func fakeGitHubMirror(t *testing.T, ghBase string, payload []byte, apiHits *int) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(payload)
	suffix, err := platformSuffix()
	if err != nil {
		t.Skip(err)
	}
	asset := "narrafork-0.9.12-" + suffix
	release := fmt.Sprintf(`{"tag_name":"v0.9.12","published_at":"2026-01-01T00:00:00Z","assets":[
		{"name":%q,"size":%d,"browser_download_url":%q,"digest":"sha256:%s"},
		{"name":"narrafork-0.9.12-SHA256SUMS","browser_download_url":%q}]}`,
		asset, len(payload), ghBase+"/releases/download/v0.9.12/"+asset, hex.EncodeToString(sum[:]),
		ghBase+"/releases/download/v0.9.12/narrafork-0.9.12-SHA256SUMS")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orig := strings.TrimPrefix(r.URL.Path, "/")
		switch {
		case orig == apiBase+"/releases/latest":
			*apiHits++
			if r.Header.Get("Authorization") != "" {
				t.Errorf("GITHUB_TOKEN leaked to the mirror")
			}
			_, _ = io.WriteString(w, release)
		case strings.HasPrefix(orig, ghBase+"/releases/download/"):
			http.ServeContent(w, r, "bin", time.Time{}, strings.NewReader(string(payload)))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestAutoFallsBackToMirror(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret-token")
	payload := []byte(strings.Repeat("mirror", 4000))
	ghBase := deadURL(t) // "GitHub" 连不上
	var apiHits int
	mirror := fakeGitHubMirror(t, ghBase, payload, &apiHits)
	defer mirror.Close()

	s := testService(t, "auto", mirror.URL+"/")
	// API 地址固定是 api.github.com：直连失败需要让它连不上，这里把直连 API 打到死端口。
	s.net.cache = map[string]cacheEntry{}
	var r ghRelease
	src, err := s.net.do(context.Background(), s.loadConfig(context.Background()), func(ctx context.Context, src string) error {
		target := apiBase + "/releases/latest"
		if src == srcGitHub {
			target = ghBase + "/api"
		}
		b, err := s.net.fetch(ctx, s.loadConfig(ctx), src, opAPI, target, 1<<20)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, &r)
	})
	if err != nil || src != srcMirror {
		t.Fatalf("api src=%s err=%v", src, err)
	}
	if got := s.net.order(s.loadConfig(context.Background())); got[0] != srcMirror {
		t.Fatalf("mirror should be remembered, order = %v", got)
	}
	info := toReleaseInfo(r)
	if info == nil || !strings.HasPrefix(info.assetURL, ghBase) {
		t.Fatalf("info = %+v (browser_download_url must stay the original GitHub URL)", info)
	}
	inst, err := s.downloadTo(context.Background(), info)
	if err != nil || inst.Source != srcMirror || inst.Checksum != "digest" {
		t.Fatalf("inst=%+v err=%v", inst, err)
	}
	if st := s.GetNetworkStatus(context.Background()); st.ActiveSource != srcMirror || st.LastSource != srcMirror {
		t.Fatalf("network status = %+v", st)
	}
	found := false
	for _, l := range s.GetLogs() {
		if strings.Contains(l.Text, "using mirror") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a log line about switching to the mirror")
	}
	// 配置变化后忘掉记住的源。
	cfg := s.loadConfig(context.Background())
	cfg.DownloadProxyMode = "system"
	if _, err := s.SaveConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if got := s.net.order(s.loadConfig(context.Background())); got[0] != srcGitHub {
		t.Fatalf("order after config change = %v", got)
	}
}

func TestAutoDoesNotFallBackOnHTTPError(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer gh.Close()
	var mirrorHits int
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mirrorHits++ }))
	defer mirror.Close()
	s := testService(t, "auto", mirror.URL+"/")
	cfg := s.loadConfig(context.Background())
	_, err := s.net.do(context.Background(), cfg, func(ctx context.Context, src string) error {
		_, err := s.net.fetch(ctx, cfg, src, opChecksum, gh.URL+"/SHA256SUMS", 1<<20)
		return err
	})
	var ne *NetError
	if !errors.As(err, &ne) || ne.Kind != "http" || ne.Status != 404 || mirrorHits != 0 {
		t.Fatalf("err=%v mirrorHits=%d", err, mirrorHits)
	}
}

func TestMirrorOnlySourceAndAPICache(t *testing.T) {
	var apiHits int
	mirror := fakeGitHubMirror(t, "https://github.com/"+Repository, []byte("x"), &apiHits)
	defer mirror.Close()
	s := testService(t, "mirror", mirror.URL+"/")
	for i := 0; i < 3; i++ {
		info, src, cached, err := s.latestFor(context.Background(), "stable")
		if err != nil || src != srcMirror || info.Version != "0.9.12" || cached != (i > 0) {
			t.Fatalf("#%d info=%+v src=%s cached=%v err=%v", i, info, src, cached, err)
		}
	}
	if apiHits != 1 {
		t.Fatalf("API hits = %d, want 1 (5-minute cache)", apiHits)
	}
}

func TestClassifyErr(t *testing.T) {
	c := testService(t, "github", "")
	cfg := c.loadConfig(context.Background())
	_, err := c.net.fetch(context.Background(), cfg, srcGitHub, opAPI, deadURL(t)+"/x", 10)
	if k := errKind(err); k != "refused" {
		t.Errorf("refused: kind = %s (%v)", k, err)
	}
	_, err = c.net.fetch(context.Background(), cfg, srcGitHub, opAPI, "http://nonexistent.invalid/x", 10)
	if k := errKind(err); k != "dns" {
		t.Errorf("dns: kind = %s (%v)", k, err)
	}
	cfg.DownloadProxyMode, cfg.DownloadProxyURL = "custom", strings.Replace(deadURL(t), "http://", "socks5://", 1)
	_, err = c.net.fetch(context.Background(), cfg, srcGitHub, opAPI, "https://example.com/", 10)
	if k := errKind(err); k != "proxy" {
		t.Errorf("proxy: kind = %s (%v)", k, err)
	}
	cfg.DownloadProxyURL = ""
	if _, err := c.net.fetch(context.Background(), cfg, srcGitHub, opAPI, "https://example.com/", 10); errKind(err) != "config" {
		t.Errorf("custom without URL must be a config error, got %v", err)
	}
}

func TestChildEnv(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://ambient:1")
	t.Setenv("no_proxy", "corp.example")
	s := testService(t, "github", "")
	envMap := func(env []string) map[string]string {
		m := map[string]string{}
		for _, kv := range env {
			if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
				m[k] = v
			}
		}
		return m
	}
	cfg := s.loadConfig(context.Background())

	cfg.NarraforkProxyMode = "inherit"
	env, _, err := s.childEnv(cfg)
	if m := envMap(env); err != nil || m["HTTPS_PROXY"] != "http://ambient:1" {
		t.Fatalf("inherit: %v %v", m["HTTPS_PROXY"], err)
	}

	cfg.NarraforkProxyMode = "none"
	env, _, _ = s.childEnv(cfg)
	for k := range envMap(env) {
		if strings.Contains(strings.ToUpper(k), "PROXY") {
			t.Fatalf("none: %s still set", k)
		}
	}

	cfg.NarraforkProxyMode, cfg.NarraforkProxyURL = "custom", "socks5h://u:p@127.0.0.1:1080"
	env, note, err := s.childEnv(cfg)
	m := envMap(env)
	if err != nil || m["HTTP_PROXY"] != "socks5h://u:p@127.0.0.1:1080" || m["all_proxy"] != m["HTTP_PROXY"] ||
		m["NO_PROXY"] != "localhost,127.0.0.1,::1" || m["no_proxy"] != m["NO_PROXY"] {
		t.Fatalf("custom: %v %v", m, err)
	}
	if strings.Contains(note, ":p@") || !strings.Contains(note, "SOCKS") {
		t.Fatalf("note must redact the password and mention SOCKS: %q", note)
	}

	cfg.NarraforkProxyMode = "download"
	cfg.DownloadProxyMode, cfg.DownloadProxyURL = "custom", "http://dl:3128"
	env, _, _ = s.childEnv(cfg)
	if m := envMap(env); m["https_proxy"] != "http://dl:3128" {
		t.Fatalf("download: %v", m)
	}

	cfg.NarraforkProxyMode, cfg.NarraforkProxyURL = "custom", ""
	if _, _, err := s.childEnv(cfg); err == nil {
		t.Fatal("custom without URL must fail")
	}
}

func TestPruneKeepsCurrentAndPrevious(t *testing.T) {
	s := New(nil, t.TempDir())
	for _, v := range []string{"0.9.8", "0.9.10", "0.9.11", "0.9.12"} {
		if err := os.MkdirAll(filepath.Join(s.binDir(), v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s.pruneVersions("0.9.12")
	for v, want := range map[string]bool{"0.9.12": true, "0.9.11": true, "0.9.10": false, "0.9.8": false} {
		_, err := os.Stat(filepath.Join(s.binDir(), v))
		if (err == nil) != want {
			t.Errorf("%s exists=%v want %v", v, err == nil, want)
		}
	}
}

// TestLiveGitHub 读取真实 GitHub Release（只读）。设置 LAUNCHER_LIVE=1 时运行。
func TestLiveGitHub(t *testing.T) {
	if os.Getenv("LAUNCHER_LIVE") != "1" {
		t.Skip("set LAUNCHER_LIVE=1")
	}
	s := testService(t, "auto", "")
	for _, ch := range []string{"stable", "beta"} {
		info, src, cached, err := s.latestFor(context.Background(), ch)
		if err != nil {
			t.Fatal(ch, err)
		}
		t.Logf("%s via %s (cached=%v): v%s %s %d bytes prerelease=%v digest=%v sums=%v", ch, src, cached, info.Version, info.AssetName, info.AssetSize, info.Prerelease, digestHex(info.digest) != "", info.sumsURL != "")
	}
	list, _, _, err := s.fetchReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d releases for this platform", len(list))
	for _, r := range s.TestNetwork(context.Background()) {
		t.Logf("network test %-14s ok=%v skipped=%v status=%d %dms proxy=%q kind=%s %s", r.ID, r.OK, r.Skipped, r.Status, r.LatencyMs, r.Proxy, r.ErrorKind, r.Error)
	}
	// 系统代理（环境变量 / 注册表）存在时，再按 system 模式测一遍。
	cfg := s.loadConfig(context.Background())
	cfg.DownloadProxyMode = "system"
	if _, err := s.SaveConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	st := s.GetNetworkStatus(context.Background())
	t.Logf("system proxy: %q (origin %s)", st.Proxy, st.ProxyOrigin)
	if st.Proxy == "" {
		return
	}
	for _, r := range s.TestNetwork(context.Background()) {
		t.Logf("network test (system proxy) %-14s ok=%v skipped=%v status=%d %dms proxy=%q kind=%s %s", r.ID, r.OK, r.Skipped, r.Status, r.LatencyMs, r.Proxy, r.ErrorKind, r.Error)
	}
}

// TestLiveMirror 经镜像读取真实发布信息、确认资产带 digest、取 SHA256SUMS，并对二进制做一次小的 Range 请求。
func TestLiveMirror(t *testing.T) {
	if os.Getenv("LAUNCHER_LIVE") != "1" {
		t.Skip("set LAUNCHER_LIVE=1")
	}
	s := testService(t, "mirror", "")
	info, src, _, err := s.latestFor(context.Background(), "stable")
	if err != nil {
		t.Fatal(err)
	}
	if src != srcMirror {
		t.Fatalf("source = %s", src)
	}
	t.Logf("v%s %s %d bytes digest=%s", info.Version, info.AssetName, info.AssetSize, info.digest)
	if digestHex(info.digest) == "" {
		t.Fatalf("asset has no sha256 digest: %q", info.digest)
	}
	if !strings.HasPrefix(info.assetURL, "https://github.com/") {
		t.Fatalf("browser_download_url = %s (expected the original GitHub URL)", info.assetURL)
	}
	cfg := s.loadConfig(context.Background())
	body, err := s.net.fetch(context.Background(), cfg, srcMirror, opChecksum, info.sumsURL, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := parseChecksums(body, info.AssetName)
	if err != nil || sum != digestHex(info.digest) {
		t.Fatalf("SHA256SUMS %s vs digest %s: %v", sum, digestHex(info.digest), err)
	}

	c, err := s.net.client(cfg, clientKey{dial: dialTimeout, header: downloadHeaderTimeout})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, mirrored(cfg, srcMirror, info.assetURL), nil)
	req.Header.Set("Range", "bytes=1024-2047")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	chunk, _ := io.ReadAll(resp.Body)
	start, size, ok := parseContentRange(resp.Header.Get("Content-Range"))
	t.Logf("range: HTTP %d Content-Range=%q len=%d", resp.StatusCode, resp.Header.Get("Content-Range"), len(chunk))
	if resp.StatusCode != http.StatusPartialContent || !ok || start != 1024 || size != info.AssetSize || len(chunk) != 1024 {
		t.Fatal("mirror does not serve byte ranges as expected")
	}
}

package launcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	r.observe(`Another NarraFork process (pid 4242) is already using ~/.narrafork`)
	if r.lockPID != 4242 {
		t.Fatalf("lockPID = %d", r.lockPID)
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
	if _, err := (Config{Host: "a b;rm"}).normalize(); err == nil {
		t.Fatal("expected invalid host")
	}
	if _, err := (Config{Port: 70000}).normalize(); err == nil {
		t.Fatal("expected invalid port")
	}
	if _, err := (Config{Host: "0.0.0.0", Port: 7779}).normalize(); err != nil {
		t.Fatal(err)
	}
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

			s := New(nil, t.TempDir())
			info := &ReleaseInfo{Version: "0.9.12", AssetName: name, AssetSize: int64(len(payload)),
				assetURL: srv.URL + "/bin", sumsURL: srv.URL + "/SHA256SUMS"}
			inst, err := s.downloadTo(context.Background(), info)
			if !tc.ok {
				if err == nil {
					t.Fatal("expected checksum error")
				}
				if _, statErr := os.Stat(filepath.Join(s.binDir(), "0.9.12", name)); statErr == nil {
					t.Fatal("bad download must not be kept")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
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
	s := New(nil, t.TempDir())
	for _, ch := range []string{"stable", "beta"} {
		info, err := s.latestFor(context.Background(), ch)
		if err != nil {
			t.Fatal(ch, err)
		}
		t.Logf("%s: v%s %s %d bytes prerelease=%v sums=%v", ch, info.Version, info.AssetName, info.AssetSize, info.Prerelease, info.sumsURL != "")
	}
	list, err := s.fetchReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d releases for this platform", len(list))
}

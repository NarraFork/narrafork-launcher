package launcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEndToEnd 下载真实的 NarraFork 稳定版、启动、等健康检查、再优雅停止。
// NarraFork 的数据目录（~/.narrafork）被重定向到临时目录，不触碰真实数据。
// 设置 LAUNCHER_E2E=1 时运行（会下载约 190MB）。
// LAUNCHER_E2E_SOURCE=auto|github|mirror 指定下载源（默认 auto）。
func TestEndToEnd(t *testing.T) {
	if os.Getenv("LAUNCHER_E2E") != "1" {
		t.Skip("set LAUNCHER_E2E=1")
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	dataDir := os.Getenv("LAUNCHER_E2E_DATA")
	if dataDir == "" {
		dataDir = t.TempDir()
	}
	s := New(nil, dataDir)
	cfg := defaultConfig()
	if src := os.Getenv("LAUNCHER_E2E_SOURCE"); src != "" {
		cfg.DownloadSource = src
	}
	cfg.Port = 17979
	cfg.Host = "127.0.0.1"
	if _, err := s.SaveConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	// 模拟 narrastore 的自定义数据目录：NARRAFORK_HOME 应经 SetExtraEnv 传给 NarraFork。
	narraHome := t.TempDir()
	SetExtraEnv(s, func() []string { return []string{"NARRAFORK_HOME=" + narraHome} })

	if inst, _ := s.readInstalled(); inst == nil {
		info, src, _, err := s.latestFor(context.Background(), "stable")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("downloading v%s (%d bytes); release info via %s, digest=%q", info.Version, info.AssetSize, src, info.digest)
		inst, err := s.download(info)
		if err != nil {
			dumpLogs(t, s)
			t.Fatal(err)
		}
		t.Logf("installed via %s, checksum basis %s", inst.Source, inst.Checksum)
		if cfg.DownloadSource == "mirror" && inst.Source != "mirror" {
			t.Errorf("source = %s, want mirror", inst.Source)
		}
	}

	if err := s.start(cfg, false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Minute)
	for s.GetStatus().State != StateRunning {
		st := s.GetStatus()
		if st.State == StateError {
			dumpLogs(t, s)
			t.Fatalf("start failed: %s", st.Error)
		}
		if time.Now().After(deadline) {
			dumpLogs(t, s)
			t.Fatal("timed out waiting for NarraFork")
		}
		time.Sleep(500 * time.Millisecond)
	}
	st := s.GetStatus()
	t.Logf("running pid=%d url=%s version=%s", st.PID, st.URL, st.Version)
	if st.URL != "http://127.0.0.1:17979" {
		t.Errorf("url = %s", st.URL)
	}
	if _, err := os.Stat(filepath.Join(narraHome, "narrafork.db")); err != nil {
		t.Errorf("NARRAFORK_HOME not honoured: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".narrafork", "narrafork.db")); err == nil {
		t.Error("database was created in the default home despite NARRAFORK_HOME")
	}

	begin := time.Now()
	s.stopProcess(30 * time.Second)
	t.Logf("stopped in %s, state=%s", time.Since(begin).Round(time.Millisecond), s.GetStatus().State)
	if s.GetStatus().State != StateStopped {
		dumpLogs(t, s)
		t.Fatalf("state after stop = %s", s.GetStatus().State)
	}
	graceful := false
	for _, l := range s.GetLogs() {
		if strings.Contains(l.Text, "Graceful shutdown timed out") {
			t.Error("graceful shutdown did not complete")
		}
		if strings.Contains(strings.ToLower(l.Text), "shutdown") || strings.Contains(strings.ToLower(l.Text), "shutting down") {
			graceful = true
		}
	}
	t.Logf("server logged shutdown: %v", graceful)
}

func dumpLogs(t *testing.T, s *Service) {
	logs := s.GetLogs()
	if len(logs) > 60 {
		logs = logs[len(logs)-60:]
	}
	for _, l := range logs {
		t.Logf("[%s] %s", l.Stream, l.Text)
	}
}

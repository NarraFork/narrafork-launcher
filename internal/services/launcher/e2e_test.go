package launcher

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestEndToEnd 下载真实的 NarraFork 稳定版、启动、等健康检查、再优雅停止。
// NarraFork 的数据目录（~/.narrafork）被重定向到临时目录，不触碰真实数据。
// 设置 LAUNCHER_E2E=1 时运行（会下载约 190MB）。
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

	if inst, _ := s.readInstalled(); inst == nil {
		info, err := s.latestFor(context.Background(), "stable")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("downloading v%s (%d bytes)", info.Version, info.AssetSize)
		if _, err := s.download(info); err != nil {
			t.Fatal(err)
		}
	}

	cfg := defaultConfig()
	cfg.Port = 17979
	cfg.Host = "127.0.0.1"
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

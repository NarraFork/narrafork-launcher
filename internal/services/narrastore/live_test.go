package narrastore

import (
	"context"
	"os"
	"testing"
)

// TestLiveScanReadOnly 只读扫描真实的 NarraFork 数据目录，用于核对分类表。
// 只调用 Scan / GetRunState，绝不删除或修改任何文件。
//
//	NARRASTORE_LIVE=1 go test ./internal/services/narrastore -run Live -v
func TestLiveScanReadOnly(t *testing.T) {
	if os.Getenv("NARRASTORE_LIVE") != "1" {
		t.Skip("set NARRASTORE_LIVE=1 to scan the real NarraFork home (read-only)")
	}
	s := New(nil, nil)
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("home=%s exists=%v total=%d bytes / %d files truncated=%v skipped=%d %dms run=%+v",
		res.Home, res.Exists, res.TotalBytes, res.TotalFiles, res.Truncated, res.Skipped, res.DurationMs, res.Run)
	for _, c := range res.Categories {
		if c.Files == 0 && len(c.Paths) == 0 {
			continue
		}
		t.Logf("%-18s %-8s stopped=%-5v deletable=%-5v %10d B %6d files %v (+%d)",
			c.ID, c.Risk, c.RequiresStopped, c.Deletable, c.Bytes, c.Files, c.Paths, c.MorePaths)
	}
}

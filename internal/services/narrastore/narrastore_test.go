package narrastore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// 所有测试只在 t.TempDir() 里构造假的数据目录，不会触碰真实的 ~/.narrafork。

type memPrefs struct {
	mu sync.Mutex
	m  map[string]string
}

func (p *memPrefs) Get(_ context.Context, k string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.m[k], nil
}

func (p *memPrefs) Set(_ context.Context, k, v string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[k] = v
	return nil
}

func (p *memPrefs) Delete(_ context.Context, k string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.m, k)
	return nil
}

// newTestService 返回指向临时数据目录的服务。running 控制注入的 isRunning。
func newTestService(t *testing.T, home string, running *bool) *Service {
	t.Helper()
	s := New(&memPrefs{m: map[string]string{}}, func() bool { return running != nil && *running })
	s.defaultHome = func() (string, bool, error) { return home, false, nil }
	userHome := t.TempDir()
	s.userHome = func() (string, error) { return userHome, nil }
	return s
}

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeHome 构造覆盖各分类的假数据目录，返回每个分类期望的 (bytes, files)。
func fakeHome(t *testing.T) (string, map[string][2]int64) {
	t.Helper()
	home := filepath.Join(t.TempDir(), ".narrafork")
	files := map[string]int{
		"server.log":                        100,
		"server.log.1":                      200,
		"server.log.2":                      300,
		"manager.log.1":                     50,
		"settings.json":                     64,
		"narrafork.db":                      4096,
		"narrafork.db-wal":                  1000,
		"narrafork.db-shm":                  32,
		"narrafork.db.bak":                  10, // 用户自己的备份：不能归入 database
		"narrafork.lock":                    20,
		"secret.key":                        32,
		"openai-models.json":                11,
		"grammars/bash.wasm":                500,
		"bin/rg.exe":                        700,
		"bin/distribution/x.bin":            70,
		"request-recordings/a/record.json":  40,
		"saved-request-recordings/n/m.json": 41,
		"uploads/n1/media/a.png":            900,
		"uploads/avatars/u/a.png":           90,
		"chat-attachments/r/1.bin":          80,
		"shares/s1/file.txt":                60,
		"tree-snapshots/abc/objects/x":      33,
		"file-change-blobs/sha256/aa/aaaa":  34,
		"updates/narrafork-1.0.0.exe":       600,
		"updates/placed-update.json":        6,
		"model-catalog/catalog.json":        12,
		"plugins/state.json":                100,
		"plugins/packages/p/1/index.js":     200,
		"packs/n/p/readme.md":               5,
		"buffers/t1.json":                   7,
		"skills/s/SKILL.md":                 8,
		"weixin/accounts/acc.json":          9,
		"weixin/tmp/x.tmp":                  13,
		"worktree-requests/j.json":          14,
		"mystery.bin":                       15, // 未知 → other
		"some-new-dir/inner.txt":            16, // 未知 → other
		"weixin/unknown.json":               17, // 容器里的未知条目 → other
	}
	for rel, size := range files {
		writeFile(t, filepath.Join(home, filepath.FromSlash(rel)), size)
	}
	// transfers 是空目录：也应出现在 paths 里，0 字节 0 文件。
	if err := os.MkdirAll(filepath.Join(home, "transfers"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := map[string][2]int64{
		"rotatedLogs":       {550, 3},
		"logs":              {100, 1},
		"settings":          {64, 1},
		"database":          {5128, 3},
		"credentials":       {32 + 9, 2},
		"caches":            {11 + 500, 2},
		"helperBinaries":    {770, 2},
		"requestRecordings": {40, 1},
		"savedRecordings":   {41, 1},
		"attachments":       {900 + 90 + 80, 3},
		"shares":            {60, 1},
		"history":           {67, 2},
		"updates":           {606, 2},
		"modelCatalog":      {12, 1},
		"plugins":           {300, 2},
		"temp":              {5 + 7 + 13, 3},
		"userContent":       {8, 1},
		"internal":          {20 + 14, 2},
		"other":             {10 + 15 + 16 + 17, 4},
		"transfers":         {0, 0},
	}
	return home, want
}

func catByID(res ScanResult, id string) CategoryStat {
	for _, c := range res.Categories {
		if c.ID == id {
			return c
		}
	}
	return CategoryStat{}
}

func TestScanCategorizesAndTotals(t *testing.T) {
	home, want := fakeHome(t)
	s := newTestService(t, home, nil)
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Exists || res.Home != home {
		t.Fatalf("home = %q exists=%v", res.Home, res.Exists)
	}
	var wantBytes, wantFiles int64
	for id, w := range want {
		c := catByID(res, id)
		if c.ID == "" {
			t.Fatalf("category %s missing", id)
		}
		if c.Bytes != w[0] || c.Files != w[1] {
			t.Errorf("%s: bytes=%d files=%d, want %d/%d (paths %v)", id, c.Bytes, c.Files, w[0], w[1], c.Paths)
		}
		wantBytes += w[0]
		wantFiles += w[1]
	}
	if res.TotalBytes != wantBytes || res.TotalFiles != wantFiles {
		t.Errorf("total = %d/%d, want %d/%d", res.TotalBytes, res.TotalFiles, wantBytes, wantFiles)
	}
	if got := catByID(res, "database").Paths; !equalSet(got, []string{"narrafork.db", "narrafork.db-wal", "narrafork.db-shm"}) {
		t.Errorf("database paths = %v", got)
	}
	if got := catByID(res, "other").Paths; !equalSet(got, []string{"narrafork.db.bak", "mystery.bin", "some-new-dir", "weixin/unknown.json"}) {
		t.Errorf("other paths = %v", got)
	}
	if got := catByID(res, "transfers").Paths; !equalSet(got, []string{"transfers"}) {
		t.Errorf("transfers paths = %v", got)
	}
	if c := catByID(res, "other"); c.Deletable {
		t.Error("other must not be deletable")
	}
	if c := catByID(res, "internal"); c.Deletable {
		t.Error("internal must not be deletable")
	}
	if c := catByID(res, "rotatedLogs"); c.RequiresStopped || c.LabelKey != "storage.categories.rotatedLogs.label" {
		t.Errorf("rotatedLogs meta = %+v", c)
	}
	if res.Truncated {
		t.Error("unexpected truncation")
	}
}

func TestScanMissingHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	s := newTestService(t, home, nil)
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Exists || res.TotalBytes != 0 || len(res.Categories) != len(categoryDefs) {
		t.Fatalf("unexpected result: %+v", res)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("scan must not create the home directory")
	}
}

func TestScanCancelledAndBounded(t *testing.T) {
	home, _ := fakeHome(t)
	s := newTestService(t, home, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Scan(ctx); err == nil {
		t.Fatal("cancelled scan should fail")
	}

	s.maxEntries = 5
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatal("entry limit should truncate the scan")
	}
}

func TestDeleteWhitelistAndConfirm(t *testing.T) {
	home, _ := fakeHome(t)
	s := newTestService(t, home, nil)
	results, err := s.Delete(context.Background(), []string{"bogus", "other", "internal", "database", "../x"}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"bogus":    ErrCodeUnknown,
		"other":    ErrCodeNotDeletable,
		"internal": ErrCodeNotDeletable,
		"database": ErrCodeConfirmRequired,
		"../x":     ErrCodeUnknown,
	}
	for _, r := range results {
		if r.OK || r.Error != want[r.ID] {
			t.Errorf("%s: ok=%v err=%q, want %q", r.ID, r.OK, r.Error, want[r.ID])
		}
	}
	for _, rel := range []string{"narrafork.db", "mystery.bin", "narrafork.lock"} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("%s should still exist: %v", rel, err)
		}
	}
}

func TestDeleteSafeCategories(t *testing.T) {
	home, _ := fakeHome(t)
	s := newTestService(t, home, nil)
	results, err := s.Delete(context.Background(), []string{"rotatedLogs", "caches", "temp", "rotatedLogs"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("duplicates should be ignored: %+v", results)
	}
	for _, r := range results {
		if !r.OK {
			t.Fatalf("%s failed: %+v", r.ID, r)
		}
	}
	if results[0].FreedBytes != 550 || results[0].RemovedFiles != 3 {
		t.Errorf("rotatedLogs freed %d bytes / %d files", results[0].FreedBytes, results[0].RemovedFiles)
	}
	gone := []string{"server.log.1", "server.log.2", "manager.log.1", "grammars", "openai-models.json", "packs", "buffers", "weixin/tmp"}
	for _, rel := range gone {
		if _, err := os.Lstat(filepath.Join(home, filepath.FromSlash(rel))); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s should be deleted", rel)
		}
	}
	kept := []string{"server.log", "settings.json", "narrafork.db", "weixin/accounts/acc.json", "weixin/unknown.json", "mystery.bin"}
	for _, rel := range kept {
		if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s should be kept: %v", rel, err)
		}
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatal("home directory itself must survive")
	}
}

func TestDeleteDangerWithConfirm(t *testing.T) {
	home, _ := fakeHome(t)
	s := newTestService(t, home, nil)
	results, err := s.Delete(context.Background(), []string{"database"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].OK || results[0].RemovedFiles != 3 {
		t.Fatalf("database delete: %+v", results[0])
	}
	if _, err := os.Stat(filepath.Join(home, "narrafork.db.bak")); err != nil {
		t.Error("narrafork.db.bak is not part of the database category and must be kept")
	}
}

func TestDeleteRefusedWhileManagedRunning(t *testing.T) {
	home, _ := fakeHome(t)
	running := true
	s := newTestService(t, home, &running)
	results, err := s.Delete(context.Background(), []string{"logs", "caches", "rotatedLogs", "database"}, true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]DeleteResult{}
	for _, r := range results {
		got[r.ID] = r
	}
	for _, id := range []string{"logs", "caches", "database"} {
		if got[id].OK || got[id].Error != ErrCodeRunning {
			t.Errorf("%s should be refused while running: %+v", id, got[id])
		}
	}
	if !got["rotatedLogs"].OK {
		t.Errorf("rotated logs can be deleted while running: %+v", got["rotatedLogs"])
	}
	if _, err := os.Stat(filepath.Join(home, "server.log")); err != nil {
		t.Error("active server.log must survive while running")
	}
	rs, _ := s.GetRunState(context.Background())
	if !rs.Running || !rs.Managed {
		t.Errorf("run state = %+v", rs)
	}
}

// writeLock 写一个 NarraFork 格式的 narrafork.lock（instance-lock.ts 的 v2 payload）。
func writeLock(t *testing.T, home string, pid int, startedAt time.Time, execPath string) {
	t.Helper()
	payload := map[string]any{
		"version": 2, "pid": pid, "token": "test-token", "dbPath": filepath.Join(home, "narrafork.db"),
		"startedAt": startedAt.UTC().Format("2006-01-02T15:04:05.000Z"), "argv": []string{"narrafork", "--serve"},
		"bootId": "boot-1", "procStartTicks": nil, "execPath": execPath, "hostname": "test", "uid": nil,
	}
	buf, _ := json.Marshal(payload)
	if err := os.WriteFile(filepath.Join(home, lockFileName), buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRefusedWhileLockHeldByLiveProcess(t *testing.T) {
	home, _ := fakeHome(t)
	s := newTestService(t, home, nil)
	// 当前测试进程就是一个确定存活的 pid；按它的真实启动时间与镜像写锁。
	self, _ := os.Executable()
	obs := observeProcess(os.Getpid())
	started := obs.startedAt
	if started.IsZero() {
		started = time.Now()
	}
	writeLock(t, home, os.Getpid(), started.Add(time.Second), self)

	rs, err := s.GetRunState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !rs.Running || rs.Managed || rs.LockPID != os.Getpid() {
		t.Fatalf("run state = %+v", rs)
	}
	results, err := s.Delete(context.Background(), []string{"caches", "rotatedLogs"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].OK || results[0].Error != ErrCodeRunning {
		t.Errorf("caches should be refused while the lock holder lives: %+v", results[0])
	}
	if !results[1].OK {
		t.Errorf("rotated logs should still be deletable: %+v", results[1])
	}
	if _, err := os.Stat(filepath.Join(home, "grammars")); err != nil {
		t.Error("caches must survive")
	}
}

func TestStaleLockDoesNotBlock(t *testing.T) {
	home, _ := fakeHome(t)
	s := newTestService(t, home, nil)

	// 已退出进程的 pid。
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	writeLock(t, home, cmd.ProcessState.Pid(), time.Now(), os.Args[0])
	if rs, _ := s.GetRunState(context.Background()); rs.Running {
		t.Fatalf("exited pid should not count as running: %+v", rs)
	}

	// pid 存活但已被复用：记录的启动时间远早于进程实际启动时间。
	self, _ := os.Executable()
	writeLock(t, home, os.Getpid(), time.Now().Add(-24*time.Hour), self)
	if obs := observeProcess(os.Getpid()); !obs.startedAt.IsZero() {
		if rs, _ := s.GetRunState(context.Background()); rs.Running {
			t.Errorf("recycled pid should not count as running: %+v", rs)
		}
	}

	// pid 存活但镜像不同。
	writeLock(t, home, os.Getpid(), time.Now(), "/opt/narrafork/definitely-not-this-test")
	if obs := observeProcess(os.Getpid()); obs.image != "" {
		if rs, _ := s.GetRunState(context.Background()); rs.Running {
			t.Errorf("different image should not count as running: %+v", rs)
		}
	}

	// 损坏的锁文件。
	if err := os.WriteFile(filepath.Join(home, lockFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rs, _ := s.GetRunState(context.Background()); rs.Running {
		t.Errorf("corrupt lock should not count as running: %+v", rs)
	}
}

func TestLockLiveRules(t *testing.T) {
	now := time.Now()
	ticks := int64(42)
	other := int64(43)
	p := lockPayload{PID: 1, StartedAt: now.UTC().Format(time.RFC3339Nano), ExecPath: `C:\x\narrafork-0.9.8-windows-x64.exe`}
	cases := []struct {
		name string
		p    lockPayload
		obs  procObservation
		want bool
	}{
		{"absent", p, procObservation{state: procAbsent}, false},
		{"unknown is conservative", p, procObservation{state: procUnknown}, true},
		{"present without identity", p, procObservation{state: procPresent}, true},
		{"matching start", p, procObservation{state: procPresent, startedAt: now.Add(-time.Second), image: `C:\x\NARRAFORK-0.9.8-windows-x64.exe`}, true},
		{"recycled pid", p, procObservation{state: procPresent, startedAt: now.Add(-time.Hour)}, false},
		{"different image", p, procObservation{state: procPresent, image: `C:\Windows\explorer.exe`}, false},
		{"zombie", p, procObservation{state: procPresent, zombie: true}, false},
		{"ticks match", lockPayload{PID: 1, ProcStartTicks: &ticks}, procObservation{state: procPresent, startTicks: &ticks}, true},
		{"ticks differ", lockPayload{PID: 1, ProcStartTicks: &ticks}, procObservation{state: procPresent, startTicks: &other}, false},
	}
	for _, c := range cases {
		if got := lockLive(c.p, c.obs); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// makeDirLink 在 link 处创建指向 target 目录的链接：Windows 上用目录联接（无需特权），
// 其它平台用符号链接。系统拒绝时跳过测试。
func makeDirLink(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if err != nil {
			t.Skipf("cannot create junction: %v %s", err, out)
		}
		return
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
}

func TestLinksAreNeverFollowed(t *testing.T) {
	home, _ := fakeHome(t)
	outside := filepath.Join(t.TempDir(), "outside")
	writeFile(t, filepath.Join(outside, "precious.txt"), 12345)
	writeFile(t, filepath.Join(outside, "sub", "more.txt"), 678)

	// 一个在可删除分类内部的联接，一个在分类顶层的联接，一个在未知位置的联接。
	makeDirLink(t, outside, filepath.Join(home, "grammars", "escape"))
	makeDirLink(t, outside, filepath.Join(home, "packs-link-unknown"))
	if err := os.RemoveAll(filepath.Join(home, "buffers")); err != nil {
		t.Fatal(err)
	}
	makeDirLink(t, outside, filepath.Join(home, "buffers"))
	// 指向数据目录自身的环。
	makeDirLink(t, home, filepath.Join(home, "temp-loop-unknown"))

	s := newTestService(t, home, nil)
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalBytes >= 12345 {
		t.Fatalf("scan followed a link out of the home: total=%d", res.TotalBytes)
	}
	if res.Skipped < 4 {
		t.Errorf("expected links to be reported as skipped, got %d", res.Skipped)
	}

	results, err := s.Delete(context.Background(), []string{"caches", "temp"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if !r.OK {
			t.Fatalf("%s: %+v", r.ID, r)
		}
	}
	for _, rel := range []string{"precious.txt", "sub/more.txt"} {
		if _, err := os.Stat(filepath.Join(outside, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("link target %s was deleted: %v", rel, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(home, "buffers")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the link entry itself should be removed")
	}
	if _, err := os.Lstat(filepath.Join(home, "packs-link-unknown")); err != nil {
		t.Error("links in unknown locations must be left alone")
	}
	if _, err := os.Stat(filepath.Join(home, "narrafork.db")); err != nil {
		t.Error("loop link must not lead to deleting the home")
	}
}

func TestHomeIsALinkItself(t *testing.T) {
	real, _ := fakeHome(t)
	link := filepath.Join(t.TempDir(), "home-link")
	makeDirLink(t, real, link)
	s := newTestService(t, link, nil)
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if catByID(res, "database").Bytes == 0 {
		t.Fatal("a linked home directory should still be scanned")
	}
	results, err := s.Delete(context.Background(), []string{"rotatedLogs"}, false)
	if err != nil || !results[0].OK {
		t.Fatalf("delete through linked home: %v %+v", err, results)
	}
	if _, err := os.Stat(filepath.Join(real, "server.log.1")); !errors.Is(err, os.ErrNotExist) {
		t.Error("rotated log should be deleted in the real home")
	}
}

func TestCheckAndSetHome(t *testing.T) {
	def := filepath.Join(t.TempDir(), ".narrafork")
	s := newTestService(t, def, nil)
	ctx := context.Background()
	userHome, _ := s.userHome()

	file := filepath.Join(t.TempDir(), "a-file")
	writeFile(t, file, 1)

	root := filepath.VolumeName(def) + string(filepath.Separator)
	if runtime.GOOS != "windows" {
		root = "/"
	}
	cases := map[string]string{
		"relative/dir":               "notAbsolute",
		"":                           "notAbsolute",
		file:                         "isFile",
		root:                         "isRoot",
		userHome:                     "isUserHome",
		filepath.Join(file, "child"): "notCreatable",
	}
	for p, code := range cases {
		c := s.CheckHome(p)
		if c.Valid || c.Error != code {
			t.Errorf("CheckHome(%q) = %+v, want error %s", p, c, code)
		}
		if _, err := s.SetHome(ctx, p); err == nil {
			t.Errorf("SetHome(%q) should fail", p)
		}
	}

	target := filepath.Join(t.TempDir(), "new", "nf-home")
	c := s.CheckHome(target)
	if !c.Valid || c.Exists || !c.Empty {
		t.Fatalf("CheckHome(new) = %+v", c)
	}
	info, err := s.SetHome(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Custom || info.Path != target || !info.Exists || info.DefaultPath != def {
		t.Fatalf("after SetHome: %+v", info)
	}
	env := ChildEnv(s)
	if len(env) != 1 || env[0] != "NARRAFORK_HOME="+target {
		t.Fatalf("ChildEnv = %v", env)
	}
	entries, _ := os.ReadDir(target)
	if len(entries) != 0 {
		t.Errorf("SetHome must not leave files behind: %v", entries)
	}

	// 已有 NarraFork 数据的目录可以直接接管。
	existing, _ := fakeHome(t)
	if c := s.CheckHome(existing); !c.Valid || !c.LooksLikeHome || c.Empty {
		t.Errorf("CheckHome(existing) = %+v", c)
	}

	// 设成默认目录 = 重置。
	if info, err := s.SetHome(ctx, def); err != nil || info.Custom {
		t.Fatalf("SetHome(default) = %+v %v", info, err)
	}
	if env := ChildEnv(s); env != nil {
		t.Fatalf("default home must not set NARRAFORK_HOME: %v", env)
	}

	if _, err := s.SetHome(ctx, target); err != nil {
		t.Fatal(err)
	}
	if info, err := s.ResetHome(ctx); err != nil || info.Custom || info.Path != def {
		t.Fatalf("ResetHome = %+v %v", info, err)
	}
}

func TestScanUsesCustomHome(t *testing.T) {
	def := filepath.Join(t.TempDir(), "default-home")
	custom, _ := fakeHome(t)
	s := newTestService(t, def, nil)
	if _, err := s.SetHome(context.Background(), custom); err != nil {
		t.Fatal(err)
	}
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Home != custom || catByID(res, "database").Bytes == 0 {
		t.Fatalf("scan should use the custom home: %+v", res.Home)
	}
}

func TestBackupDatabase(t *testing.T) {
	home, _ := fakeHome(t)
	running := false
	s := newTestService(t, home, &running)
	ctx := context.Background()
	dest := t.TempDir()

	if _, err := s.BackupDatabase(ctx, filepath.Join(home, "backups")); err == nil {
		t.Error("backup into the data directory must be refused")
	}
	if _, err := s.BackupDatabase(ctx, "relative"); err == nil {
		t.Error("relative backup directory must be refused")
	}
	running = true
	if _, err := s.BackupDatabase(ctx, dest); err == nil {
		t.Error("backup while running must be refused")
	}
	running = false
	res, err := s.BackupDatabase(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(res.Files)
	if strings.Join(res.Files, ",") != "narrafork.db,narrafork.db-shm,narrafork.db-wal" || res.Bytes != 5128 {
		t.Fatalf("backup = %+v", res)
	}
	if !within(dest, res.Dir) {
		t.Fatalf("backup dir %s not under %s", res.Dir, dest)
	}
	if _, err := os.Stat(filepath.Join(home, "narrafork.db")); err != nil {
		t.Fatal("backup must not move the source")
	}
}

func TestWithin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	if within(root, root) {
		t.Error("root is not within itself")
	}
	if !within(root, filepath.Join(root, "a", "b")) {
		t.Error("child should be within")
	}
	if within(root, filepath.Join(root, "..", "home2")) || within(root, filepath.Dir(root)) {
		t.Error("siblings / parents are not within")
	}
	if within(root, root+"2") {
		t.Error("prefix siblings are not within")
	}
}

func equalSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// Package narrastore 管理 NarraFork 的数据目录（NARRAFORK_HOME，默认 ~/.narrafork）：
// 按分类统计占用、按分类删除、切换数据目录、备份数据库。
//
// 安全约束：
//   - 只删除 categories.go 白名单里标记为可删除的分类；未知条目归入 other，只统计不删除
//   - 只删除数据目录内的条目，绝不删除数据目录本身；遍历与删除都不跟随符号链接 / 目录联接
//   - 需要停止 NarraFork 的分类在它运行时拒绝删除：既看启动器托管的进程状态，也看 NarraFork
//     自己的实例锁 narrafork.lock（覆盖在启动器之外启动的 NarraFork）
//   - danger 分类还要求调用方显式确认（confirmDanger）
package narrastore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"narrafork-launcher/internal/utils/logx"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	// homeEnv 与 NarraFork server/lib/narrafork-home.ts 一致。
	homeEnv = "NARRAFORK_HOME"
	// homePrefKey 是自定义数据目录在 preferences 表中的键（值为 JSON 字符串，空 = 默认）。
	homePrefKey = "narrastore.home"
	// defaultHomeName 是 NarraFork 默认数据目录名（~/.narrafork）。
	defaultHomeName = ".narrafork"
)

// PrefStore 是 preferences.Service 中本服务用到的部分。
type PrefStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	Delete(ctx context.Context, key string) error
}

// Service 是 Wails 服务。
type Service struct {
	prefs     PrefStore
	isRunning func() bool
	app       *application.App
	log       *slog.Logger

	// sem 串行化扫描 / 删除 / 备份：同一时刻只有一个遍历或删除在进行。
	sem chan struct{}

	// 以下字段供测试替换。
	defaultHome func() (path string, fromEnv bool, err error)
	userHome    func() (string, error)
	scanBudget  time.Duration
	maxEntries  int
}

// New 创建服务。isRunning 报告启动器托管的 NarraFork 是否在运行（可为 nil）。
func New(prefs PrefStore, isRunning func() bool) *Service {
	return &Service{
		prefs:       prefs,
		isRunning:   isRunning,
		log:         logx.For("narrastore"),
		sem:         make(chan struct{}, 1),
		defaultHome: defaultHomePath,
		userHome:    os.UserHomeDir,
		scanBudget:  5 * time.Minute,
		maxEntries:  2_000_000,
	}
}

// Attach 在 application.New 之后回填 app（RevealHome 需要）。写成包级函数，避免被 Wails 生成为前端可调用方法。
func Attach(s *Service, app *application.App) {
	s.app = app
}

// ChildEnv 返回启动 NarraFork 子进程时要追加的环境变量：使用自定义数据目录时为
// ["NARRAFORK_HOME=<dir>"]，默认目录时为 nil。追加在 os.Environ() 之后即可覆盖继承的同名变量。
// 写成包级函数，避免被 Wails 生成为前端可调用方法。
func ChildEnv(s *Service) []string {
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	custom, err := s.customHome(ctx)
	if err != nil {
		s.log.Warn("read custom NarraFork home failed; using default", "err", err)
		return nil
	}
	if custom == "" {
		return nil
	}
	return []string{homeEnv + "=" + custom}
}

func (s *Service) acquire(ctx context.Context) (func(), error) {
	// select 在多个 case 同时就绪时随机挑选，先查一次，已取消的请求不会拿到锁。
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case s.sem <- struct{}{}:
		return func() { <-s.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// —— 数据目录 ——

// HomeInfo 描述 NarraFork 数据目录。
type HomeInfo struct {
	Path           string `json:"path"`           // 生效中的数据目录（下次启动 NarraFork 时使用）
	Exists         bool   `json:"exists"`         // 目录是否存在
	Custom         bool   `json:"custom"`         // 是否为启动器里设置的自定义目录
	DefaultPath    string `json:"defaultPath"`    // 不设置自定义目录时使用的目录
	DefaultFromEnv bool   `json:"defaultFromEnv"` // 默认目录来自启动器进程的 NARRAFORK_HOME 环境变量
}

// defaultHomePath 与 NarraFork 的 getNarraforkHome() 一致：NARRAFORK_HOME（去空白）优先，否则 ~/.narrafork。
// 相对路径按用户主目录解析——启动器以用户主目录为工作目录启动 NarraFork。
func defaultHomePath() (string, bool, error) {
	if v := strings.TrimSpace(os.Getenv(homeEnv)); v != "" {
		if !filepath.IsAbs(v) {
			if h, err := os.UserHomeDir(); err == nil {
				v = filepath.Join(h, v)
			}
		}
		return filepath.Clean(v), true, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	return filepath.Join(h, defaultHomeName), false, nil
}

func (s *Service) customHome(ctx context.Context) (string, error) {
	if s.prefs == nil {
		return "", nil
	}
	raw, err := s.prefs.Get(ctx, homePrefKey)
	if err != nil {
		return "", err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	// 前端 PreferencesService 约定值为 JSON；兼容直接写入的裸路径。
	var v string
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		v = raw
	}
	v = strings.TrimSpace(v)
	if v == "" || !filepath.IsAbs(v) {
		return "", nil
	}
	return filepath.Clean(v), nil
}

func (s *Service) resolveHome(ctx context.Context) (HomeInfo, error) {
	def, fromEnv, err := s.defaultHome()
	if err != nil {
		return HomeInfo{}, fmt.Errorf("resolve default NarraFork home: %w", err)
	}
	info := HomeInfo{Path: def, DefaultPath: def, DefaultFromEnv: fromEnv}
	custom, err := s.customHome(ctx)
	if err != nil {
		return HomeInfo{}, err
	}
	if custom != "" {
		info.Path = custom
		info.Custom = true
	}
	if st, err := os.Stat(info.Path); err == nil && st.IsDir() {
		info.Exists = true
	}
	return info, nil
}

// GetHome 返回当前生效的 NarraFork 数据目录。
func (s *Service) GetHome(ctx context.Context) (HomeInfo, error) {
	return s.resolveHome(ctx)
}

// HomeCheck 是对候选数据目录的预检结果，供界面在确认前提示。
type HomeCheck struct {
	Path          string `json:"path"`          // 规范化后的路径
	Valid         bool   `json:"valid"`         // 可以设为数据目录
	Error         string `json:"error"`         // 不可用时的原因代码：notAbsolute / isFile / isRoot / isUserHome / notCreatable
	Exists        bool   `json:"exists"`        // 目录已存在
	Empty         bool   `json:"empty"`         // 目录不存在或为空
	LooksLikeHome bool   `json:"looksLikeHome"` // 已包含 NarraFork 数据（narrafork.db / settings.json）
	IsDefault     bool   `json:"isDefault"`     // 与默认目录相同
}

// CheckHome 预检一个候选数据目录，不做任何修改。
func (s *Service) CheckHome(path string) HomeCheck {
	c := HomeCheck{Path: strings.TrimSpace(path)}
	if c.Path == "" || !filepath.IsAbs(c.Path) {
		c.Error = "notAbsolute"
		return c
	}
	c.Path = filepath.Clean(c.Path)
	if isFilesystemRoot(c.Path) {
		c.Error = "isRoot"
		return c
	}
	if h, err := s.userHome(); err == nil && samePath(h, c.Path) {
		c.Error = "isUserHome"
		return c
	}
	if def, _, err := s.defaultHome(); err == nil {
		c.IsDefault = samePath(def, c.Path)
	}
	st, err := os.Stat(c.Path)
	switch {
	case err == nil && !st.IsDir():
		c.Error = "isFile"
		return c
	case err == nil:
		c.Exists = true
		entries, rerr := os.ReadDir(c.Path)
		c.Empty = rerr == nil && len(entries) == 0
		c.LooksLikeHome = looksLikeHome(c.Path)
	case errors.Is(err, os.ErrNotExist):
		c.Empty = true
		if !creatable(c.Path) {
			c.Error = "notCreatable"
			return c
		}
	default:
		c.Error = "notCreatable"
		return c
	}
	c.Valid = true
	return c
}

// SetHome 把 NarraFork 数据目录设为 path（下次启动 NarraFork 时生效；不迁移已有数据）。
// 目录不存在时会创建并验证可写。与默认目录相同时等同于 ResetHome。
func (s *Service) SetHome(ctx context.Context, path string) (HomeInfo, error) {
	c := s.CheckHome(path)
	if !c.Valid {
		return HomeInfo{}, fmt.Errorf("invalid data directory (%s): %s", c.Error, c.Path)
	}
	if c.IsDefault {
		return s.ResetHome(ctx)
	}
	if err := preflightWritable(c.Path); err != nil {
		return HomeInfo{}, fmt.Errorf("data directory is not writable: %w", err)
	}
	if s.prefs == nil {
		return HomeInfo{}, errors.New("preferences unavailable")
	}
	buf, _ := json.Marshal(c.Path)
	if err := s.prefs.Set(ctx, homePrefKey, string(buf)); err != nil {
		return HomeInfo{}, err
	}
	s.log.Info("NarraFork home set", "path", c.Path)
	return s.resolveHome(ctx)
}

// ResetHome 恢复默认数据目录（下次启动 NarraFork 时生效）。
func (s *Service) ResetHome(ctx context.Context) (HomeInfo, error) {
	if s.prefs != nil {
		if err := s.prefs.Delete(ctx, homePrefKey); err != nil {
			return HomeInfo{}, err
		}
	}
	s.log.Info("NarraFork home reset to default")
	return s.resolveHome(ctx)
}

// RevealHome 在文件管理器中打开数据目录（不存在时先创建，与 NarraFork 一样用 0700）。
func (s *Service) RevealHome(ctx context.Context) error {
	info, err := s.resolveHome(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(info.Path, 0o700); err != nil {
		return err
	}
	if s.app == nil {
		return errors.New("application not attached")
	}
	return s.app.Browser.OpenFile(filepath.Clean(info.Path))
}

// —— 路径工具 ——

func isFilesystemRoot(p string) bool {
	p = filepath.Clean(p)
	return filepath.Dir(p) == p
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// within 报告 target 是否严格位于 root 之内（不含 root 本身）。纯词法判断，链接检查由调用方负责。
func within(root, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || rel == "." || rel == "" {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// creatable 报告不存在的 p 能否被创建：最近的已存在祖先必须是目录。
func creatable(p string) bool {
	for cur := filepath.Dir(p); ; cur = filepath.Dir(cur) {
		st, err := os.Stat(cur)
		if err == nil {
			return st.IsDir()
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false
		}
		if filepath.Dir(cur) == cur {
			return false
		}
	}
}

func preflightWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".narrafork-launcher-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// homeMarkers 是 NarraFork 数据目录的标志文件。
var homeMarkers = []string{"narrafork.db", "settings.json", "narrafork.lock", "server.log"}

func looksLikeHome(dir string) bool {
	for _, m := range homeMarkers {
		if _, err := os.Lstat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

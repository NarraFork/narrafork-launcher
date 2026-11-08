package narrastore

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	// maxPathsPerCategory 限制每个分类返回给界面的路径数，其余计入 MorePaths。
	maxPathsPerCategory = 50
	// maxDepth 是遍历深度上限，超过即停止下探并标记 Truncated。
	maxDepth = 128
	// readBatch 是每次 ReadDir 读取的条目数：大目录分批读，内存只与子目录数相关。
	readBatch = 512
)

// CategoryStat 是一个分类的统计结果。
type CategoryStat struct {
	ID              string   `json:"id"`
	LabelKey        string   `json:"labelKey"`        // i18n key：storage.categories.<id>.label
	DescriptionKey  string   `json:"descriptionKey"`  // i18n key：storage.categories.<id>.description
	Risk            string   `json:"risk"`            // safe | caution | danger
	RequiresStopped bool     `json:"requiresStopped"` // 删除前必须停止 NarraFork
	Deletable       bool     `json:"deletable"`       // 允许经 Delete 删除
	Bytes           int64    `json:"bytes"`
	Files           int64    `json:"files"`
	Paths           []string `json:"paths"`     // 命中的条目（相对数据目录，正斜杠）
	MorePaths       int      `json:"morePaths"` // Paths 之外还有多少条
}

// ScanResult 是一次扫描的结果。
type ScanResult struct {
	Home       string         `json:"home"`
	Exists     bool           `json:"exists"`
	TotalBytes int64          `json:"totalBytes"`
	TotalFiles int64          `json:"totalFiles"`
	Categories []CategoryStat `json:"categories"`
	// Truncated 为 true 表示达到时间 / 条目 / 深度上限而提前停止，数值是下限。
	Truncated bool `json:"truncated"`
	// Skipped 是未跟随的符号链接 / 目录联接 / 其它重解析点，以及无法读取的目录数。
	Skipped    int64    `json:"skipped"`
	ScannedAt  int64    `json:"scannedAt"` // unix 毫秒
	DurationMs int64    `json:"durationMs"`
	Run        RunState `json:"run"`
}

func newCategoryStat(d *categoryDef) CategoryStat {
	return CategoryStat{
		ID:              d.id,
		LabelKey:        "storage.categories." + d.id + ".label",
		DescriptionKey:  "storage.categories." + d.id + ".description",
		Risk:            d.risk,
		RequiresStopped: d.requiresStopped,
		Deletable:       d.deletable,
		Paths:           []string{},
	}
}

func (c *CategoryStat) addPath(rel string) {
	if len(c.Paths) < maxPathsPerCategory {
		c.Paths = append(c.Paths, rel)
	} else {
		c.MorePaths++
	}
}

// Scan 按分类统计数据目录占用。只读，不跟随链接；受时间 / 条目 / 深度上限约束，前端取消即停止。
func (s *Service) Scan(ctx context.Context) (ScanResult, error) {
	release, err := s.acquire(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	defer release()

	info, err := s.resolveHome(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	started := time.Now()
	res := ScanResult{Home: info.Path, Exists: info.Exists}
	stats := make([]CategoryStat, len(categoryDefs))
	byID := make(map[string]*CategoryStat, len(categoryDefs))
	for i := range categoryDefs {
		stats[i] = newCategoryStat(&categoryDefs[i])
		byID[categoryDefs[i].id] = &stats[i]
	}

	if info.Exists {
		root, err := filepath.EvalSymlinks(info.Path)
		if err != nil {
			return ScanResult{}, err
		}
		sc := &scanner{
			ctx:        ctx,
			deadline:   started.Add(s.scanBudget),
			maxEntries: s.maxEntries,
			cls:        defaultClassifier,
			byID:       byID,
		}
		if err := sc.classifyDir(root, "", 0); err != nil && !errors.Is(err, errScanLimit) {
			return ScanResult{}, err
		}
		res.Truncated = sc.truncated
		res.Skipped = sc.skipped
	}

	for _, c := range stats {
		res.TotalBytes += c.Bytes
		res.TotalFiles += c.Files
	}
	res.Categories = stats
	res.ScannedAt = time.Now().UnixMilli()
	res.DurationMs = time.Since(started).Milliseconds()
	res.Run = s.runState(info.Path)
	s.log.Info("NarraFork home scanned", "home", info.Path, "bytes", res.TotalBytes,
		"files", res.TotalFiles, "truncated", res.Truncated, "ms", res.DurationMs)
	return res, nil
}

// errScanLimit 表示达到扫描上限：停止遍历，但保留已统计的结果。
var errScanLimit = errors.New("scan limit reached")

type scanner struct {
	ctx        context.Context
	deadline   time.Time
	maxEntries int
	entries    int
	truncated  bool
	skipped    int64
	cls        *classifier
	byID       map[string]*CategoryStat
}

// tick 在每个条目上调用：检查取消与上限。
func (sc *scanner) tick() error {
	sc.entries++
	if sc.entries%256 == 0 {
		if err := sc.ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(sc.deadline) {
			sc.truncated = true
			return errScanLimit
		}
	}
	if sc.maxEntries > 0 && sc.entries > sc.maxEntries {
		sc.truncated = true
		return errScanLimit
	}
	return nil
}

// classifyDir 逐个分类 dir 的子项；容器目录（如 weixin）继续逐项分类，其余整棵子树计入所属分类。
func (sc *scanner) classifyDir(dir, rel string, depth int) error {
	var subdirs []fs.DirEntry
	err := readDirBatched(dir, func(e fs.DirEntry) error {
		if err := sc.tick(); err != nil {
			return err
		}
		childRel := joinRel(rel, e.Name())
		id, descend := sc.cls.classify(childRel)
		if descend && isRealDir(e.Type()) {
			subdirs = append(subdirs, e)
			return nil
		}
		cat := sc.byID[id]
		cat.addPath(childRel)
		return sc.account(e, filepath.Join(dir, e.Name()), cat, depth+1)
	})
	if err != nil {
		return sc.dirError(err)
	}
	for _, e := range subdirs {
		if err := sc.classifyDir(filepath.Join(dir, e.Name()), joinRel(rel, e.Name()), depth+1); err != nil {
			return err
		}
	}
	return nil
}

// account 把一个条目（目录则整棵子树）计入分类。
func (sc *scanner) account(e fs.DirEntry, abs string, cat *CategoryStat, depth int) error {
	t := e.Type()
	if isRealDir(t) {
		return sc.measureDir(abs, cat, depth)
	}
	if t&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		// 链接 / 联接 / 云文件占位等重解析点：只计条目本身，不跟随。
		sc.skipped++
		cat.Files++
		return nil
	}
	cat.Files++
	if t.IsRegular() {
		if fi, err := e.Info(); err == nil {
			cat.Bytes += fi.Size()
		}
	}
	return nil
}

func (sc *scanner) measureDir(dir string, cat *CategoryStat, depth int) error {
	if depth > maxDepth {
		sc.truncated = true
		return nil
	}
	var subdirs []string
	err := readDirBatched(dir, func(e fs.DirEntry) error {
		if err := sc.tick(); err != nil {
			return err
		}
		if isRealDir(e.Type()) {
			subdirs = append(subdirs, e.Name())
			return nil
		}
		return sc.account(e, filepath.Join(dir, e.Name()), cat, depth+1)
	})
	if err != nil {
		return sc.dirError(err)
	}
	for _, name := range subdirs {
		if err := sc.measureDir(filepath.Join(dir, name), cat, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// dirError 把无法读取的目录记为 skipped 继续扫描；取消与上限照常向上传递。
func (sc *scanner) dirError(err error) error {
	if errors.Is(err, errScanLimit) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	sc.skipped++
	return nil
}

// isRealDir 报告条目是否为真实目录（不是符号链接、目录联接或其它重解析点）。
// Windows 上 Go 把符号链接与联接报告为非目录（联接带 ModeIrregular），云文件占位目录带 ModeIrregular。
func isRealDir(t fs.FileMode) bool {
	return t.IsDir() && t&(fs.ModeSymlink|fs.ModeIrregular) == 0
}

// readDirBatched 分批读取目录项并逐个回调；回调返回错误即停止。
func readDirBatched(dir string, fn func(fs.DirEntry) error) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		batch, err := f.ReadDir(readBatch)
		for _, e := range batch {
			if cbErr := fn(e); cbErr != nil {
				return cbErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
	}
}

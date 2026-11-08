package narrastore

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Delete 的失败原因代码（前端映射为 storage.deleteError.<code>）。
const (
	ErrCodeUnknown         = "unknown"         // 不在白名单
	ErrCodeNotDeletable    = "notDeletable"    // 分类只统计不删除
	ErrCodeRunning         = "running"         // NarraFork 正在运行
	ErrCodeConfirmRequired = "confirmRequired" // danger 分类未确认
	ErrCodeFailed          = "failed"          // 部分或全部条目删除失败
	ErrCodeCancelled       = "cancelled"       // 调用方取消
)

// DeleteResult 是单个分类的删除结果。
type DeleteResult struct {
	ID           string `json:"id"`
	OK           bool   `json:"ok"`
	Error        string `json:"error"`   // 失败原因代码，成功为空
	Message      string `json:"message"` // 底层错误信息（仅 failed）
	Entries      int    `json:"entries"` // 命中的顶层条目数
	RemovedFiles int64  `json:"removedFiles"`
	FreedBytes   int64  `json:"freedBytes"`
}

// Delete 删除指定分类。ids 必须是白名单中可删除的分类；需要停止 NarraFork 的分类在它运行时拒绝；
// danger 分类还要求 confirmDanger=true。每个分类单独返回结果，一个失败不影响其它分类。
func (s *Service) Delete(ctx context.Context, ids []string, confirmDanger bool) ([]DeleteResult, error) {
	release, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	info, err := s.resolveHome(ctx)
	if err != nil {
		return nil, err
	}
	var root string
	if info.Exists {
		if root, err = filepath.EvalSymlinks(info.Path); err != nil {
			return nil, err
		}
	}

	results := make([]DeleteResult, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		r := DeleteResult{ID: id}
		def := findCategory(id)
		switch {
		case def == nil:
			r.Error = ErrCodeUnknown
		case !def.deletable:
			r.Error = ErrCodeNotDeletable
		case def.risk == RiskDanger && !confirmDanger:
			r.Error = ErrCodeConfirmRequired
		case ctx.Err() != nil:
			r.Error = ErrCodeCancelled
		// 每个分类删除前重新检查：NarraFork 可能在此期间被启动。
		case (def.requiresStopped || def.risk == RiskDanger) && s.runState(info.Path).Running:
			r.Error = ErrCodeRunning
		case root == "":
			r.OK = true // 数据目录不存在：没有可删除的内容
		default:
			s.deleteCategory(ctx, root, def, &r)
		}
		if r.Error != "" {
			s.log.Warn("delete NarraFork data refused or failed", "category", id, "code", r.Error, "msg", r.Message)
		} else {
			s.log.Info("NarraFork data deleted", "category", id, "files", r.RemovedFiles, "bytes", r.FreedBytes)
		}
		results = append(results, r)
	}
	return results, nil
}

func (s *Service) deleteCategory(ctx context.Context, root string, def *categoryDef, r *DeleteResult) {
	matches, err := collectMatches(ctx, root, def.id, s.maxEntries)
	if err != nil {
		r.Error, r.Message = errorCode(err), err.Error()
		return
	}
	r.Entries = len(matches)
	d := &deleter{ctx: ctx}
	for _, rel := range matches {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		// 双重保险：条目必须严格位于数据目录之内，且绝不是数据目录本身。
		if !within(root, abs) {
			d.fail(errors.New("refusing to delete outside the data directory: " + abs))
			continue
		}
		if err := d.remove(abs, 0); err != nil {
			if isCancel(err) {
				break
			}
			d.fail(err)
		}
	}
	r.RemovedFiles, r.FreedBytes = d.files, d.bytes
	switch {
	case ctx.Err() != nil:
		r.Error = ErrCodeCancelled
	case d.firstErr != nil:
		r.Error, r.Message = ErrCodeFailed, d.firstErr.Error()
	default:
		r.OK = true
	}
}

// collectMatches 列出属于分类 id 的条目（相对数据目录，正斜杠）。只下探真实目录，不跟随链接。
func collectMatches(ctx context.Context, root, id string, maxEntries int) ([]string, error) {
	var out []string
	entries := 0
	var walk func(dir, rel string) error
	walk = func(dir, rel string) error {
		var subdirs []string
		err := readDirBatched(dir, func(e fs.DirEntry) error {
			entries++
			if maxEntries > 0 && entries > maxEntries {
				return errScanLimit
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			childRel := joinRel(rel, e.Name())
			cid, descend := defaultClassifier.classify(childRel)
			if descend && isRealDir(e.Type()) {
				subdirs = append(subdirs, e.Name())
				return nil
			}
			if cid == id {
				out = append(out, childRel)
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, name := range subdirs {
			if err := walk(filepath.Join(dir, name), joinRel(rel, name)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, ""); err != nil {
		return nil, err
	}
	return out, nil
}

// deleter 递归删除且从不跟随链接：真实目录逐项删除后再删目录本身；符号链接、目录联接与其它
// 重解析点只删除条目本身（os.Remove 删除的是链接，不是目标）。
type deleter struct {
	ctx      context.Context
	files    int64
	bytes    int64
	firstErr error
}

func (d *deleter) fail(err error) {
	if d.firstErr == nil {
		d.firstErr = err
	}
}

func (d *deleter) remove(abs string, depth int) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	fi, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if isRealDir(fi.Mode()) {
		if depth > maxDepth {
			return errors.New("directory nesting too deep: " + abs)
		}
		var names []string
		if err := readDirBatched(abs, func(e fs.DirEntry) error {
			names = append(names, e.Name())
			return nil
		}); err != nil {
			return err
		}
		for _, name := range names {
			if err := d.remove(filepath.Join(abs, name), depth+1); err != nil {
				if isCancel(err) {
					return err
				}
				d.fail(err)
			}
		}
		if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	d.files++
	if fi.Mode().IsRegular() {
		d.bytes += fi.Size()
	}
	return nil
}

func isCancel(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func errorCode(err error) string {
	if isCancel(err) {
		return ErrCodeCancelled
	}
	return ErrCodeFailed
}

// —— 数据库备份 ——

// BackupResult 是 BackupDatabase 的结果。
type BackupResult struct {
	Dir   string   `json:"dir"`   // 本次备份所在目录
	Files []string `json:"files"` // 已复制的文件名
	Bytes int64    `json:"bytes"`
}

// BackupDatabase 把 narrafork.db 与 -wal / -shm 一起复制到 destDir 下的新子目录。
// 仅在 NarraFork 停止时允许（运行中复制会得到不一致的快照）。
func (s *Service) BackupDatabase(ctx context.Context, destDir string) (BackupResult, error) {
	release, err := s.acquire(ctx)
	if err != nil {
		return BackupResult{}, err
	}
	defer release()

	info, err := s.resolveHome(ctx)
	if err != nil {
		return BackupResult{}, err
	}
	if s.runState(info.Path).Running {
		return BackupResult{}, errors.New("NarraFork is running; stop it before backing up the database")
	}
	destDir = strings.TrimSpace(destDir)
	if destDir == "" || !filepath.IsAbs(destDir) {
		return BackupResult{}, errors.New("backup directory must be an absolute path")
	}
	destDir = filepath.Clean(destDir)
	if samePath(destDir, info.Path) || within(info.Path, destDir) {
		return BackupResult{}, errors.New("backup directory must be outside the NarraFork data directory")
	}
	src := filepath.Join(info.Path, "narrafork.db")
	if fi, err := os.Lstat(src); err != nil || !fi.Mode().IsRegular() {
		return BackupResult{}, errors.New("narrafork.db not found in " + info.Path)
	}

	target := filepath.Join(destDir, "narrafork-backup-"+backupStamp())
	if err := os.MkdirAll(target, 0o700); err != nil {
		return BackupResult{}, err
	}
	res := BackupResult{Dir: target, Files: []string{}}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		name := "narrafork.db" + suffix
		fi, err := os.Lstat(filepath.Join(info.Path, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return res, err
		}
		if !fi.Mode().IsRegular() {
			continue
		}
		n, err := copyFile(ctx, filepath.Join(info.Path, name), filepath.Join(target, name))
		if err != nil {
			return res, err
		}
		res.Files = append(res.Files, name)
		res.Bytes += n
	}
	s.log.Info("NarraFork database backed up", "dir", target, "files", res.Files, "bytes", res.Bytes)
	return res, nil
}

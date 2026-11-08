package narrastore

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"narrafork-launcher/internal/utils/filex"
)

// lockFileName 与 NarraFork server/lib/instance-lock.ts 的 getInstanceLockPath 一致（数据库同目录）。
const lockFileName = "narrafork.lock"

// lockStartTolerance 与 instance-lock.ts 的 LEGACY_START_TOLERANCE_MS 一致：记录的启动时间与进程
// 实际启动时间相差超过它，说明 pid 已被复用、原进程早已退出。
const lockStartTolerance = 120 * time.Second

// lockPayload 是 narrafork.lock 中本服务关心的字段（instance-lock.ts 的 InstanceLockPayload）。
type lockPayload struct {
	PID            int    `json:"pid"`
	StartedAt      string `json:"startedAt"`
	ExecPath       string `json:"execPath"`
	ProcStartTicks *int64 `json:"procStartTicks"`
}

// RunState 描述 NarraFork 是否在使用数据目录。
type RunState struct {
	Running bool `json:"running"` // Managed || LockPID > 0
	Managed bool `json:"managed"` // 启动器托管的进程处于 starting / running / stopping
	LockPID int  `json:"lockPid"` // narrafork.lock 记录且仍存活的进程；0 = 无
}

func (s *Service) runState(home string) RunState {
	var rs RunState
	if s.isRunning != nil && s.isRunning() {
		rs.Managed = true
	}
	if pid, live := lockHolder(home); live {
		rs.LockPID = pid
	}
	rs.Running = rs.Managed || rs.LockPID > 0
	return rs
}

// GetRunState 返回 NarraFork 当前是否在使用数据目录（含在启动器之外启动的实例）。
func (s *Service) GetRunState(ctx context.Context) (RunState, error) {
	info, err := s.resolveHome(ctx)
	if err != nil {
		return RunState{}, err
	}
	return s.runState(info.Path), nil
}

// procObservation 是对一个 pid 的观察结果。
type procObservation struct {
	state      procState
	startedAt  time.Time // 进程启动时间；零值 = 未知
	image      string    // 可执行文件路径或名称；空 = 未知
	startTicks *int64    // Linux /proc/<pid>/stat 的 starttime
	zombie     bool
}

type procState int

const (
	procAbsent procState = iota
	procPresent
	procUnknown
)

// lockHolder 读取 narrafork.lock，判断记录的进程是否仍是那个 NarraFork。
// 规则对照 instance-lock.ts 的 classifyLockHolder，但在无法判定时偏向"仍在运行"：
// 这里的结论用于阻止删除，宁可多拒绝一次，也不能在 NarraFork 运行时删掉它的数据。
func lockHolder(home string) (pid int, live bool) {
	p, ok := readLock(filepath.Join(home, lockFileName))
	if !ok {
		return 0, false
	}
	return p.PID, lockLive(p, observeProcess(p.PID))
}

func readLock(path string) (lockPayload, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return lockPayload{}, false
	}
	raw, err := filex.ReadLimit(path, 64<<10)
	if err != nil {
		return lockPayload{}, false
	}
	var p lockPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.PID <= 0 {
		return lockPayload{}, false
	}
	return p, true
}

func lockLive(p lockPayload, obs procObservation) bool {
	switch obs.state {
	case procAbsent:
		return false
	case procUnknown:
		return true
	}
	if obs.zombie {
		return false
	}
	if p.ProcStartTicks != nil && obs.startTicks != nil {
		return *p.ProcStartTicks == *obs.startTicks
	}
	if p.ExecPath != "" && obs.image != "" && !sameImage(p.ExecPath, obs.image) {
		return false
	}
	if started, err := time.Parse(time.RFC3339Nano, p.StartedAt); err == nil && !obs.startedAt.IsZero() {
		drift := obs.startedAt.Sub(started)
		if drift < 0 {
			drift = -drift
		}
		return drift <= lockStartTolerance
	}
	return true
}

// sameImage 比较两个可执行文件的文件名（忽略大小写与 .exe），对应 instance-lock.ts 的镜像名比较。
func sameImage(a, b string) bool {
	norm := func(s string) string {
		s = strings.ToLower(filepath.Base(strings.ReplaceAll(s, "\\", "/")))
		return strings.TrimSuffix(s, ".exe")
	}
	return norm(a) == norm(b)
}

// —— 文件复制 ——

func copyFile(ctx context.Context, src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, &ctxReader{ctx: ctx, r: in})
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return n, err
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func backupStamp() string {
	return time.Now().Format("20060102-150405")
}

//go:build !windows

package narrastore

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// observeProcess 用 kill(pid, 0) 判断进程是否存在；Linux 上再读 /proc/<pid>/stat 的启动时刻与状态。
func observeProcess(pid int) procObservation {
	if pid <= 0 {
		return procObservation{state: procAbsent}
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
	case errors.Is(err, syscall.ESRCH):
		return procObservation{state: procAbsent}
	default:
		return procObservation{state: procUnknown}
	}
	obs := procObservation{state: procPresent}
	if raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		// comm 可能含空格与括号，从最后一个 ')' 之后开始解析：state 为第 3 字段，starttime 为第 22 字段。
		s := string(raw)
		if i := strings.LastIndexByte(s, ')'); i >= 0 {
			fields := strings.Fields(s[i+1:])
			if len(fields) > 19 {
				obs.zombie = fields[0] == "Z"
				if v, err := strconv.ParseInt(fields[19], 10, 64); err == nil {
					obs.startTicks = &v
				}
			}
		}
	}
	return obs
}

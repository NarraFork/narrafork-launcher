//go:build windows

package narrastore

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

// stillActive 是 GetExitCodeProcess 对仍在运行的进程返回的 STILL_ACTIVE。
const stillActive = 259

// observeProcess 用 Win32 API 观察 pid：是否存在、创建时间、可执行文件路径。
func observeProcess(pid int) procObservation {
	if pid <= 0 || pid > 1<<31-1 {
		return procObservation{state: procAbsent}
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return procObservation{state: procAbsent}
		}
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			// 进程存在，但属于其他用户 / 受保护：身份无法核对。
			return procObservation{state: procPresent}
		}
		return procObservation{state: procUnknown}
	}
	defer windows.CloseHandle(h)

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err == nil && code != stillActive {
		return procObservation{state: procAbsent}
	}
	obs := procObservation{state: procPresent}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err == nil {
		obs.startedAt = time.Unix(0, created.Nanoseconds())
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err == nil {
		obs.image = windows.UTF16ToString(buf[:n])
	}
	return obs
}

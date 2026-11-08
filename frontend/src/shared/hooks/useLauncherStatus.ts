import { useEffect, useState } from 'react';
import { EMPTY_STATUS, LauncherService, type LauncherStatus } from '@/services/launcher';

// 订阅 NarraFork 服务状态：mount 时拉一次快照，之后跟随 launcher:status 事件。
// 多个组件同时使用时各自订阅（事件是广播，开销可忽略）。
export const useLauncherStatus = (): LauncherStatus => {
  const [status, setStatus] = useState<LauncherStatus>(EMPTY_STATUS);

  useEffect(() => {
    let alive = true;
    LauncherService.getStatus()
      .then((s) => {
        if (alive) setStatus(s);
      })
      .catch(() => {});
    const off = LauncherService.onStatus((s) => setStatus(s));
    return () => {
      alive = false;
      off();
    };
  }, []);

  return status;
};

// 运行时长（秒），每秒刷新；未运行返回 null。
export const useUptime = (startedAt: number, running: boolean): number | null => {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!running) return;
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [running]);
  if (!running || !startedAt) return null;
  return Math.max(0, Math.floor((now - startedAt) / 1000));
};

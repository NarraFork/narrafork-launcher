import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { LauncherService, type LogLine } from '@/services/launcher';
import { useLauncherStatus, useUptime } from '@/shared/hooks/useLauncherStatus';
import { formatLine, type FormattedLine } from './ansi';

const MAX_LINES = 3000;

export interface ConsoleLine extends FormattedLine {
  seq: number;
}

// 状态 → i18n key 与徽标色调（View 负责翻译）
const STATE_KEY: Record<string, string> = {
  stopped: 'console.state.stopped',
  starting: 'console.state.starting',
  running: 'console.state.running',
  stopping: 'console.state.stopping',
  error: 'console.state.error',
};

export const useConsolePage = () => {
  const status = useLauncherStatus();
  const [lines, setLines] = useState<LogLine[]>([]);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [follow, setFollow] = useState(true);
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let alive = true;
    LauncherService.getLogs()
      .then((initial) => {
        if (!alive) return;
        // 历史与事件可能重叠：按 seq 去重合并
        setLines((cur) => {
          const seen = new Set(initial.map((l) => l.seq));
          return [...initial, ...cur.filter((l) => !seen.has(l.seq))].slice(-MAX_LINES);
        });
      })
      .catch(() => {});
    const off = LauncherService.onLog((batch) =>
      setLines((cur) => {
        const next = cur.concat(batch);
        return next.length > MAX_LINES ? next.slice(-MAX_LINES) : next;
      }),
    );
    return () => {
      alive = false;
      off();
    };
  }, []);

  const formatted = useMemo<ConsoleLine[]>(
    () => lines.map((l) => ({ seq: l.seq, ...formatLine(l.text, l.stream) })),
    [lines],
  );

  // 停在底部时跟随新输出；用户向上滚动后暂停跟随
  useEffect(() => {
    const el = scrollRef.current;
    if (el && follow) el.scrollTop = el.scrollHeight;
  }, [formatted, follow]);

  const onScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 24);
  }, []);

  const run = useCallback(async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setActionError(null);
    try {
      await fn();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, []);

  const clear = useCallback(async () => {
    await LauncherService.clearLogs().catch(() => {});
    setLines([]);
  }, []);

  const running = status.state === 'running';
  const uptime = useUptime(status.startedAt, running);
  const transitioning = status.state === 'starting' || status.state === 'stopping' || status.preparing;

  return {
    status,
    stateKey: STATE_KEY[status.state] ?? STATE_KEY.stopped!,
    running,
    transitioning,
    uptime,
    busy,
    actionError: actionError ?? (status.state === 'error' ? status.error : null),
    lines: formatted,
    scrollRef,
    onScroll,
    follow,
    jumpToEnd: () => setFollow(true),
    clear,
    start: () => run(LauncherService.start),
    stop: () => run(LauncherService.stop),
    restart: () => run(LauncherService.restart),
    open: () => run(LauncherService.open),
    openExternal: () => run(LauncherService.openExternal),
  };
};

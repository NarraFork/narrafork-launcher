import { useCallback, useEffect, useRef, useState } from 'react';
import { LauncherService, type LogLine } from '@/services/launcher';
import { usePreferences } from '@/preferences';
import { useLauncherStatus, useUptime } from '@/shared/hooks/useLauncherStatus';
import { formatLine, type FormattedLine } from './ansi';

const MAX_LINES = 3000;

export interface ConsoleLine extends FormattedLine {
  seq: number;
}

// 终端配色：auto 跟随主题（--fd-term-*），light / dark 强制浅色 / 深色终端
export const TERMINAL_SCHEMES = ['auto', 'light', 'dark'] as const;
export type TerminalScheme = (typeof TERMINAL_SCHEMES)[number];
const TERMINAL_SCHEME_PREF = 'console.terminalScheme';

const isTerminalScheme = (v: string): v is TerminalScheme =>
  (TERMINAL_SCHEMES as readonly string[]).includes(v);

// 状态 → i18n key 与徽标色调（View 负责翻译）
const STATE_KEY: Record<string, string> = {
  stopped: 'console.state.stopped',
  starting: 'console.state.starting',
  running: 'console.state.running',
  stopping: 'console.state.stopping',
  error: 'console.state.error',
};

// 每行只在到达时格式化一次（按 seq 缓存）：新批次只格式化新行，已有行保持同一对象引用，
// View 里的 memo 行组件因此不会随每个批次重渲染全部 3000 行。
const toConsoleLine = (l: LogLine): ConsoleLine => ({ seq: l.seq, ...formatLine(l.text, l.stream, l.time) });

export const useConsolePage = () => {
  const status = useLauncherStatus();
  const { preferences, setPreference } = usePreferences();
  const [lines, setLines] = useState<ConsoleLine[]>([]);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [follow, setFollow] = useState(true);
  const scrollRef = useRef<HTMLDivElement>(null);
  const cacheRef = useRef(new Map<number, ConsoleLine>());

  useEffect(() => {
    let alive = true;
    const cache = cacheRef.current;
    const format = (batch: LogLine[]): ConsoleLine[] =>
      batch.map((l) => {
        let line = cache.get(l.seq);
        if (!line) {
          line = toConsoleLine(l);
          cache.set(l.seq, line);
        }
        return line;
      });
    // 只保留仍在窗口内的缓存项
    const trim = (next: ConsoleLine[]): ConsoleLine[] => {
      const kept = next.length > MAX_LINES ? next.slice(-MAX_LINES) : next;
      if (cache.size > kept.length) {
        const live = new Set(kept.map((l) => l.seq));
        for (const seq of cache.keys()) if (!live.has(seq)) cache.delete(seq);
      }
      return kept;
    };

    LauncherService.getLogs()
      .then((initial) => {
        if (!alive) return;
        const history = format(initial);
        // 历史与事件可能重叠：按 seq 去重合并
        setLines((cur) => {
          const seen = new Set(history.map((l) => l.seq));
          return trim([...history, ...cur.filter((l) => !seen.has(l.seq))]);
        });
      })
      .catch(() => {});
    const off = LauncherService.onLog((batch) => {
      const next = format(batch);
      setLines((cur) => trim(cur.concat(next)));
    });
    return () => {
      alive = false;
      off();
    };
  }, []);

  // 停在底部时跟随新输出；用户向上滚动后暂停跟随
  useEffect(() => {
    const el = scrollRef.current;
    if (el && follow) el.scrollTop = el.scrollHeight;
  }, [lines, follow]);

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
    cacheRef.current.clear();
    setLines([]);
  }, []);

  const storedScheme = preferences[TERMINAL_SCHEME_PREF];
  const terminalScheme: TerminalScheme = isTerminalScheme(storedScheme) ? storedScheme : 'auto';
  const setTerminalScheme = useCallback(
    (value: string) => {
      if (isTerminalScheme(value)) setPreference(TERMINAL_SCHEME_PREF, value);
    },
    [setPreference],
  );

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
    lines,
    scrollRef,
    onScroll,
    follow,
    jumpToEnd: () => setFollow(true),
    clear,
    terminalScheme,
    terminalSchemes: TERMINAL_SCHEMES.map((value) => ({
      value,
      labelKey: `console.terminalScheme.${value}`,
    })),
    setTerminalScheme,
    start: () => run(LauncherService.start),
    stop: () => run(LauncherService.stop),
    restart: () => run(LauncherService.restart),
    open: () => run(LauncherService.open),
    openExternal: () => run(LauncherService.openExternal),
  };
};

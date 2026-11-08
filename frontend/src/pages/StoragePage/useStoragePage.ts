import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  NarraStoreService,
  type DeleteErrorCode,
  type HomeCheck,
  type HomeInfo,
  type RunState,
  type ScanResult,
  type StorageCategory,
} from '@/services/narrastore';
import { useLauncherStatus } from '@/shared/hooks/useLauncherStatus';

export type StorageFeedback =
  | { kind: 'idle' }
  | { kind: 'deleting'; category: StorageCategory }
  | { kind: 'deleted'; category: StorageCategory; freedBytes: number; removedFiles: number }
  | { kind: 'deleteFailed'; category: StorageCategory; code: DeleteErrorCode; message: string }
  | { kind: 'changingHome' }
  | { kind: 'homeChanged' }
  | { kind: 'homeReset' }
  | { kind: 'backingUp' }
  | { kind: 'failed'; message: string };

// 删除按钮的可用性：disabled 时 reasonKey 是给用户看的原因（i18n key），null 表示无需解释（如空分类）。
export interface DeleteAvailability {
  disabled: boolean;
  reasonKey: string | null;
}

const RUN_POLL_MS = 5000;
const EMPTY_RUN: RunState = { running: false, managed: false, lockPid: 0 };

const errText = (e: unknown): string => (e instanceof Error ? e.message : String(e));
const isCancelError = (e: unknown): boolean => e instanceof Error && e.name === 'CancelError';

export const useStoragePage = () => {
  const status = useLauncherStatus();
  const [home, setHome] = useState<HomeInfo | null>(null);
  const [scan, setScan] = useState<ScanResult | null>(null);
  const [scanning, setScanning] = useState(false);
  const [scanError, setScanError] = useState<string | null>(null);
  const [runState, setRunState] = useState<RunState>(EMPTY_RUN);
  const [feedback, setFeedback] = useState<StorageFeedback>({ kind: 'idle' });
  const [showEmpty, setShowEmpty] = useState(false);
  const aliveRef = useRef(true);
  const cancelScanRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    aliveRef.current = true;
    return () => {
      aliveRef.current = false;
      cancelScanRef.current?.();
    };
  }, []);

  const refreshHome = useCallback(async () => {
    try {
      const h = await NarraStoreService.getHome();
      if (aliveRef.current) setHome(h);
    } catch (e) {
      if (aliveRef.current) setFeedback({ kind: 'failed', message: errText(e) });
    }
  }, []);

  const refreshRunState = useCallback(async () => {
    try {
      const r = await NarraStoreService.getRunState();
      if (aliveRef.current) setRunState(r);
    } catch {
      // 保留上次的值
    }
  }, []);

  const rescan = useCallback(async () => {
    cancelScanRef.current?.();
    const handle = NarraStoreService.scan();
    cancelScanRef.current = handle.cancel;
    setScanning(true);
    setScanError(null);
    try {
      const res = await handle.result;
      if (!aliveRef.current) return;
      setScan(res);
      setRunState(res.run);
    } catch (e) {
      if (aliveRef.current && !isCancelError(e)) setScanError(errText(e));
    } finally {
      if (cancelScanRef.current === handle.cancel) {
        cancelScanRef.current = null;
        if (aliveRef.current) setScanning(false);
      }
    }
  }, []);

  const cancelScan = useCallback(() => {
    cancelScanRef.current?.();
  }, []);

  useEffect(() => {
    void refreshHome();
    void rescan();
  }, [refreshHome, rescan]);

  // NarraFork 也可能在启动器之外启动 / 退出：定时复查实例锁；托管进程状态变化时立即复查。
  useEffect(() => {
    void refreshRunState();
    const id = window.setInterval(() => void refreshRunState(), RUN_POLL_MS);
    return () => window.clearInterval(id);
  }, [refreshRunState, status.state]);

  const managedRunning = status.state === 'starting' || status.state === 'running' || status.state === 'stopping';
  const running = managedRunning || runState.running;
  const externalPid = !managedRunning && runState.lockPid > 0 ? runState.lockPid : 0;
  const runningPid = managedRunning ? status.pid : runState.lockPid;

  const busy =
    feedback.kind === 'deleting' || feedback.kind === 'changingHome' || feedback.kind === 'backingUp';

  const categories = useMemo(() => scan?.categories ?? [], [scan]);
  const nonEmpty = useMemo(() => categories.filter((c) => c.files > 0 || c.paths.length > 0), [categories]);
  const visibleCategories = showEmpty ? categories : nonEmpty;
  const emptyCount = categories.length - nonEmpty.length;
  // 环形图按大小降序。
  const chartCategories = useMemo(
    () => categories.filter((c) => c.bytes > 0).sort((a, b) => b.bytes - a.bytes),
    [categories],
  );

  const deleteAvailability = useCallback(
    (c: StorageCategory): DeleteAvailability => {
      if (!c.deletable) return { disabled: true, reasonKey: 'storage.blocked.notDeletable' };
      if (c.paths.length === 0 && c.files === 0) return { disabled: true, reasonKey: null };
      if (running && (c.requiresStopped || c.risk === 'danger')) {
        return { disabled: true, reasonKey: 'storage.blocked.running' };
      }
      return { disabled: busy || scanning, reasonKey: null };
    },
    [running, busy, scanning],
  );

  // 调用方（View）负责在此之前完成确认对话框；danger 分类必须已经过确认。
  const deleteCategory = useCallback(
    async (c: StorageCategory, confirmed: boolean) => {
      setFeedback({ kind: 'deleting', category: c });
      try {
        const [r] = await NarraStoreService.delete([c.id], confirmed);
        if (!aliveRef.current) return;
        if (r?.ok) {
          setFeedback({ kind: 'deleted', category: c, freedBytes: r.freedBytes, removedFiles: r.removedFiles });
        } else {
          setFeedback({ kind: 'deleteFailed', category: c, code: r?.error || 'failed', message: r?.message ?? '' });
        }
      } catch (e) {
        if (aliveRef.current) setFeedback({ kind: 'failed', message: errText(e) });
      }
      void refreshHome();
      void rescan();
    },
    [refreshHome, rescan],
  );

  const checkHome = useCallback((path: string): Promise<HomeCheck> => NarraStoreService.checkHome(path), []);

  const changeHome = useCallback(
    async (path: string) => {
      setFeedback({ kind: 'changingHome' });
      try {
        const h = await NarraStoreService.setHome(path);
        if (!aliveRef.current) return;
        setHome(h);
        setFeedback({ kind: h.custom ? 'homeChanged' : 'homeReset' });
      } catch (e) {
        if (aliveRef.current) setFeedback({ kind: 'failed', message: errText(e) });
      }
      void rescan();
    },
    [rescan],
  );

  const resetHome = useCallback(async () => {
    setFeedback({ kind: 'changingHome' });
    try {
      const h = await NarraStoreService.resetHome();
      if (!aliveRef.current) return;
      setHome(h);
      setFeedback({ kind: 'homeReset' });
    } catch (e) {
      if (aliveRef.current) setFeedback({ kind: 'failed', message: errText(e) });
    }
    void rescan();
  }, [rescan]);

  // 成功返回备份结果（View 弹出提示），失败抛错由 View 展示。
  const backupDatabase = useCallback(async (destDir: string) => {
    setFeedback({ kind: 'backingUp' });
    try {
      return await NarraStoreService.backupDatabase(destDir);
    } finally {
      if (aliveRef.current) setFeedback({ kind: 'idle' });
    }
  }, []);

  const revealHome = useCallback(() => {
    void NarraStoreService.revealHome().catch((e) => {
      if (aliveRef.current) setFeedback({ kind: 'failed', message: errText(e) });
    });
  }, []);

  const hasDatabase = categories.some((c) => c.id === 'database' && c.files > 0);

  return {
    home,
    scan,
    scanning,
    scanError,
    feedback,
    busy,
    running,
    runningPid,
    externalPid,
    categories: visibleCategories,
    chartCategories,
    emptyCount,
    showEmpty,
    hasDatabase,
    setShowEmpty,
    deleteAvailability,
    rescan,
    cancelScan,
    deleteCategory,
    checkHome,
    changeHome,
    resetHome,
    backupDatabase,
    revealHome,
  };
};

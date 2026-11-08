import { useCallback, useEffect, useRef, useState } from 'react';
import { NativeDialogs } from '@/services';
import { LauncherService, type LauncherConfig } from '@/services/launcher';

export type SaveState = 'idle' | 'saving' | 'saved' | 'error';

// 启动器设置 ViewModel：每次修改立即保存（与旧启动器一致），端口 / 主机在失焦时提交。
export const useLauncherSettings = () => {
  const [config, setConfig] = useState<LauncherConfig | null>(null);
  const [saveState, setSaveState] = useState<SaveState>('idle');
  const [error, setError] = useState<string | null>(null);
  const [clearing, setClearing] = useState(false);
  const [clearMessageKey, setClearMessageKey] = useState<string | null>(null);
  const latest = useRef<LauncherConfig | null>(null);

  useEffect(() => {
    LauncherService.getConfig()
      .then((c) => {
        latest.current = c;
        setConfig(c);
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, []);

  const update = useCallback(async (patch: Partial<LauncherConfig>) => {
    if (!latest.current) return;
    const next = { ...latest.current, ...patch };
    latest.current = next;
    setConfig(next);
    setSaveState('saving');
    setError(null);
    try {
      const saved = await LauncherService.saveConfig(next);
      latest.current = saved;
      setConfig(saved);
      setSaveState('saved');
    } catch (e) {
      setSaveState('error');
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const browseBinary = useCallback(
    async (title: string, filterLabel: string) => {
      const path = await NativeDialogs.openFile({
        title,
        filters: [{ displayName: filterLabel, pattern: '*' }],
      });
      if (path) await update({ binaryPath: path });
    },
    [update],
  );

  const run = useCallback(async (fn: () => Promise<void>, okKey: string) => {
    setClearing(true);
    setClearMessageKey(null);
    setError(null);
    try {
      await fn();
      setClearMessageKey(okKey);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setClearing(false);
    }
  }, []);

  return {
    config,
    saveState,
    error,
    update,
    browseBinary,
    clearing,
    clearMessageKey,
    clearSiteData: () => run(LauncherService.clearBrowsingData, 'settings.launcher.data.clearDone'),
    resetWebview: async (confirm: { title: string; message: string; okLabel: string; cancelLabel: string }) => {
      if (!(await NativeDialogs.confirm(confirm))) return;
      await run(LauncherService.resetWebviewData, 'settings.launcher.data.resetScheduled');
    },
  };
};

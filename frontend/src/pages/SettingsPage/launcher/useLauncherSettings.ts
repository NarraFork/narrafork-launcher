import { useCallback, useEffect, useRef, useState } from 'react';
import { NativeDialogs } from '@/services';
import {
  LauncherService,
  toLauncherError,
  type LauncherConfig,
  type LauncherError,
  type NetworkStatus,
  type NetworkTestResult,
} from '@/services/launcher';

export type SaveState = 'idle' | 'saving' | 'saved' | 'error';

const CONFIG_ERROR_KINDS = new Set(['invalidPort', 'invalidHost', 'invalidMirrorUrl', 'invalidProxyUrl']);

// 失焦提交的非受控输入。
const TEXT_FIELDS = ['host', 'mirrorUrl', 'downloadProxyUrl', 'narraforkProxyUrl'] as const;

const hostOf = (raw: string): string => {
  try {
    return new URL(raw).host;
  } catch {
    return raw;
  }
};

/** 一行网络测试结果：View 用 key + vars 调 t()。 */
export interface TestRow {
  id: NetworkTestResult['id'];
  state: 'ok' | 'fail' | 'skip';
  key: string;
  vars: Record<string, string | number>;
  reasonKey: string | null;
  detail: string;
}

const toTestRow = (r: NetworkTestResult): TestRow => {
  if (r.skipped) return { id: r.id, state: 'skip', key: 'settings.launcher.network.test.skipped', vars: {}, reasonKey: null, detail: '' };
  if (r.ok) {
    return r.proxy
      ? { id: r.id, state: 'ok', key: 'settings.launcher.network.test.okVia', vars: { ms: r.latencyMs, proxy: r.proxy }, reasonKey: null, detail: '' }
      : { id: r.id, state: 'ok', key: 'settings.launcher.network.test.ok', vars: { ms: r.latencyMs }, reasonKey: null, detail: '' };
  }
  return {
    id: r.id,
    state: 'fail',
    key: r.status ? 'settings.launcher.network.test.failedStatus' : 'settings.launcher.network.test.failed',
    vars: { status: r.status },
    reasonKey: `settings.launcher.network.kinds.${r.errorKind || 'network'}`,
    detail: r.error,
  };
};

// 启动器设置 ViewModel：每次修改立即保存（与旧启动器一致），端口 / 主机 / 地址类输入在失焦时提交。
export const useLauncherSettings = () => {
  const [config, setConfig] = useState<LauncherConfig | null>(null);
  const [saveState, setSaveState] = useState<SaveState>('idle');
  const [error, setError] = useState<LauncherError | null>(null);
  const [clearing, setClearing] = useState(false);
  const [clearMessageKey, setClearMessageKey] = useState<string | null>(null);
  const [network, setNetwork] = useState<NetworkStatus | null>(null);
  const [testing, setTesting] = useState(false);
  const [testResults, setTestResults] = useState<NetworkTestResult[] | null>(null);
  const [testError, setTestError] = useState<LauncherError | null>(null);
  // 地址输入框的版本号：保存失败后递增，让非受控输入重新挂载回到已保存的值。
  const [inputsVersion, setInputsVersion] = useState(0);
  const latest = useRef<LauncherConfig | null>(null);

  const refreshNetwork = useCallback(() => {
    LauncherService.getNetworkStatus()
      .then(setNetwork)
      .catch(() => {});
  }, []);

  useEffect(() => {
    LauncherService.getConfig()
      .then((c) => {
        latest.current = c;
        setConfig(c);
      })
      .catch((e) => setError(toLauncherError(e)));
    refreshNetwork();
  }, [refreshNetwork]);

  const update = useCallback(
    async (patch: Partial<LauncherConfig>) => {
      if (!latest.current) return;
      const previous = latest.current;
      const next = { ...previous, ...patch };
      latest.current = next;
      setConfig(next);
      setSaveState('saving');
      setError(null);
      try {
        const saved = await LauncherService.saveConfig(next);
        latest.current = saved;
        setConfig(saved);
        setSaveState('saved');
        // 后端规范化了地址类输入（补协议 / 结尾斜杠）时，让非受控输入显示规范化后的值。
        if (TEXT_FIELDS.some((k) => saved[k] !== next[k])) setInputsVersion((v) => v + 1);
        refreshNetwork();
      } catch (e) {
        // 不合法的值不保留在界面上，回到上次保存成功的配置。
        latest.current = previous;
        setConfig(previous);
        setInputsVersion((v) => v + 1);
        setSaveState('error');
        setError(toLauncherError(e));
      }
    },
    [refreshNetwork],
  );

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
      setError(toLauncherError(e));
    } finally {
      setClearing(false);
    }
  }, []);

  const testNetwork = useCallback(async () => {
    setTesting(true);
    setTestError(null);
    try {
      setTestResults(await LauncherService.testNetwork());
    } catch (e) {
      setTestError(toLauncherError(e));
    } finally {
      setTesting(false);
      refreshNetwork();
    }
  }, [refreshNetwork]);

  return {
    config,
    saveState,
    error,
    /** 已知配置错误的 i18n key；其它错误为 null，由 View 直接显示 error.message。 */
    errorKey: error && CONFIG_ERROR_KINDS.has(error.kind) ? `settings.launcher.errors.${error.kind}` : null,
    update,
    browseBinary,
    clearing,
    clearMessageKey,
    network,
    /** 下一次请求首先使用的源（auto 模式会话内可能已切到镜像）。 */
    activeSource: network?.activeSource || null,
    lastSource: network?.lastSource || null,
    mirrorHost: hostOf(config?.mirrorUrl ?? ''),
    testing,
    testRows: testResults?.map(toTestRow) ?? null,
    testError,
    testNetwork,
    inputsVersion,
    clearSiteData: () => run(LauncherService.clearBrowsingData, 'settings.launcher.data.clearDone'),
    resetWebview: async (confirm: { title: string; message: string; okLabel: string; cancelLabel: string }) => {
      if (!(await NativeDialogs.confirm(confirm))) return;
      await run(LauncherService.resetWebviewData, 'settings.launcher.data.resetScheduled');
    },
  };
};

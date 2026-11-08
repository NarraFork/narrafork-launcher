import { useCallback, useEffect, useState } from 'react';
import {
  LauncherService,
  toLauncherError,
  type ActiveSource,
  type DownloadProgress,
  type LauncherError,
  type NetworkStatus,
  type ReleaseInfo,
  type UpdateCheck,
} from '@/services/launcher';
import { useLauncherStatus } from '@/shared/hooks/useLauncherStatus';

const NET_KINDS = new Set([
  'dns',
  'timeout',
  'refused',
  'reset',
  'tls',
  'proxy',
  'network',
  'stalled',
  'http',
  'rateLimit',
  'badResponse',
  'checksum',
  'config',
]);

/** 给 View 的可读错误：kindKey 是分类文案（如"连接超时"），sourceKey 指出哪个源，message 是后端原文。 */
export interface ReadableError {
  kindKey: string | null;
  opKey: string | null;
  source: ActiveSource;
  host: string;
  message: string;
}

const readable = (e: LauncherError): ReadableError => ({
  kindKey: NET_KINDS.has(e.kind) ? `versions.errors.kinds.${e.kind}` : null,
  opKey: e.op ? `versions.errors.ops.${e.op}` : null,
  source: e.source,
  host: e.host,
  message: e.message,
});

const fromProgress = (p: DownloadProgress): ReadableError =>
  readable({ message: p.error, kind: p.errorKind, op: 'download', source: p.source, host: p.host, status: 0, field: '' });

const hostOf = (raw: string): string => {
  try {
    return new URL(raw).host;
  } catch {
    return raw;
  }
};

export const useVersionsPage = () => {
  const status = useLauncherStatus();
  const [check, setCheck] = useState<UpdateCheck | null>(null);
  const [checking, setChecking] = useState(false);
  const [checkError, setCheckError] = useState<ReadableError | null>(null);
  const [releases, setReleases] = useState<ReleaseInfo[]>([]);
  const [releasesError, setReleasesError] = useState<ReadableError | null>(null);
  const [progress, setProgress] = useState<DownloadProgress | null>(null);
  const [downloadError, setDownloadError] = useState<ReadableError | null>(null);
  const [dataDir, setDataDir] = useState('');
  const [network, setNetwork] = useState<NetworkStatus | null>(null);

  const refreshNetwork = useCallback(() => {
    LauncherService.getNetworkStatus()
      .then(setNetwork)
      .catch(() => {});
  }, []);

  const refresh = useCallback(async () => {
    setChecking(true);
    setCheckError(null);
    setReleasesError(null);
    const [c, r] = await Promise.allSettled([LauncherService.checkUpdate(), LauncherService.listReleases()]);
    if (c.status === 'fulfilled') setCheck(c.value);
    else setCheckError(readable(toLauncherError(c.reason)));
    if (r.status === 'fulfilled') setReleases(r.value);
    else setReleasesError(readable(toLauncherError(r.reason)));
    setChecking(false);
    refreshNetwork();
  }, [refreshNetwork]);

  useEffect(() => {
    void refresh();
    LauncherService.dataDir().then(setDataDir).catch(() => {});
    return LauncherService.onDownload((p) => {
      setProgress(p);
      if (p.phase === 'error') setDownloadError(fromProgress(p));
      if (p.phase === 'done' || p.phase === 'error') refreshNetwork();
      if (p.phase === 'done') {
        setDownloadError(null);
        void LauncherService.checkUpdate().then(setCheck).catch(() => {});
      }
    });
  }, [refresh, refreshNetwork]);

  const install = useCallback(async (version: string) => {
    setDownloadError(null);
    try {
      await LauncherService.install(version);
    } catch (e) {
      const err = toLauncherError(e);
      // 取消不算错误；下载阶段的错误已经由进度事件带上来源信息，这里只补"拿发布信息"阶段的失败。
      if (err.kind === 'cancelled' || /cancel/i.test(err.message)) return;
      setDownloadError((prev) => prev ?? readable(err));
    }
  }, []);

  const downloading =
    status.downloading ||
    progress?.phase === 'downloading' ||
    progress?.phase === 'retrying' ||
    progress?.phase === 'verifying';
  const percent =
    progress && progress.total > 0 ? Math.min(100, Math.round((progress.received / progress.total) * 100)) : 0;
  const mirrorHost = hostOf(network?.mirrorUrl ?? '');

  return {
    status,
    check,
    checking,
    checkError,
    releases,
    releasesError,
    progress,
    percent,
    downloading,
    downloadError,
    dataDir,
    network,
    mirrorHost,
    /** 本次会话下一次请求将使用的源；auto 模式下可能已切到镜像。 */
    activeSource: (network?.activeSource || null) as ActiveSource | null,
    refresh,
    install,
    cancel: () => void LauncherService.cancelDownload(),
    restart: () => void LauncherService.restart(),
    openRelease: (url: string) => void LauncherService.openURL(url),
    reveal: () => void LauncherService.revealDownloads().catch(() => {}),
    installed: check?.current || status.installed,
    running: status.state === 'running',
    runningVersion: status.version,
  };
};

import { useCallback, useEffect, useState } from 'react';
import {
  LauncherService,
  type DownloadProgress,
  type ReleaseInfo,
  type UpdateCheck,
} from '@/services/launcher';
import { useLauncherStatus } from '@/shared/hooks/useLauncherStatus';

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

export const useVersionsPage = () => {
  const status = useLauncherStatus();
  const [check, setCheck] = useState<UpdateCheck | null>(null);
  const [checking, setChecking] = useState(false);
  const [checkError, setCheckError] = useState<string | null>(null);
  const [releases, setReleases] = useState<ReleaseInfo[]>([]);
  const [releasesError, setReleasesError] = useState<string | null>(null);
  const [progress, setProgress] = useState<DownloadProgress | null>(null);
  const [downloadError, setDownloadError] = useState<string | null>(null);
  const [dataDir, setDataDir] = useState('');

  const refresh = useCallback(async () => {
    setChecking(true);
    setCheckError(null);
    setReleasesError(null);
    const [c, r] = await Promise.allSettled([LauncherService.checkUpdate(), LauncherService.listReleases()]);
    if (c.status === 'fulfilled') setCheck(c.value);
    else setCheckError(errText(c.reason));
    if (r.status === 'fulfilled') setReleases(r.value);
    else setReleasesError(errText(r.reason));
    setChecking(false);
  }, []);

  useEffect(() => {
    void refresh();
    LauncherService.dataDir().then(setDataDir).catch(() => {});
    return LauncherService.onDownload((p) => {
      setProgress(p);
      if (p.phase === 'error') setDownloadError(p.error);
      if (p.phase === 'done') {
        setDownloadError(null);
        void LauncherService.checkUpdate().then(setCheck).catch(() => {});
      }
    });
  }, [refresh]);

  const install = useCallback(async (version: string) => {
    setDownloadError(null);
    try {
      await LauncherService.install(version);
    } catch (e) {
      const msg = errText(e);
      // 取消不算错误
      if (!/cancel/i.test(msg)) setDownloadError(msg);
    }
  }, []);

  const downloading = status.downloading || progress?.phase === 'downloading' || progress?.phase === 'verifying';
  const percent =
    progress && progress.total > 0 ? Math.min(100, Math.round((progress.received / progress.total) * 100)) : 0;

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

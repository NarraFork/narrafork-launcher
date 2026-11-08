import { Badge, Button, Progress, Text } from '@mantine/core';
import { IconDownload, IconExternalLink, IconFolderOpen, IconRefresh, IconX } from '@tabler/icons-react';
import { useT } from '@/i18n';
import type { ActiveSource } from '@/services/launcher';
import { formatBytes } from '@/shared/format';
import { useVersionsPage, type ReadableError } from './useVersionsPage';
import classes from './VersionsPage.module.css';

// 版本：当前 / 最新版本、下载进度、可下载版本列表（对应旧启动器的"更新"页）。
export const VersionsPage = () => {
  const t = useT();
  const vm = useVersionsPage();
  const latest = vm.check?.latest ?? null;
  const pendingRestart = vm.running && vm.runningVersion && vm.installed && vm.runningVersion !== vm.installed;

  const sourceName = (src: ActiveSource, host = '') =>
    src === 'mirror'
      ? t('versions.source.mirror', { host: host || vm.mirrorHost })
      : src === 'github'
        ? t('versions.source.github')
        : t('versions.source.unknown');

  const renderError = (err: ReadableError) => (
    <div className={classes.errorBox}>
      <Text className={classes.error}>
        {err.opKey ? t(err.opKey) : t('versions.errors.generic')}
        {err.source && ` · ${sourceName(err.source, err.host)}`}
        {err.kindKey && ` · ${t(err.kindKey)}`}
      </Text>
      {err.kindKey && <Text className={classes.errorHint}>{t(`${err.kindKey}Hint`)}</Text>}
      <Text className={classes.errorDetail}>{err.message}</Text>
    </div>
  );

  const progressLabel = () => {
    const p = vm.progress!;
    if (p.phase === 'verifying') return t('versions.verifying');
    if (p.phase === 'retrying') {
      return t('versions.retrying', {
        attempt: p.attempt,
        reason: p.errorKind ? t(`versions.errors.kinds.${p.errorKind}`) : p.error,
      });
    }
    return t('versions.downloading', {
      version: p.version,
      received: formatBytes(p.received),
      total: formatBytes(p.total),
      speed: formatBytes(p.bytesPerSec),
    });
  };

  return (
    <div className={classes.root}>
      <div className={classes.body}>
        <section className={classes.card}>
          <div className={classes.cardHeader}>
            <Text className={classes.cardTitle}>{t('versions.summary.title')}</Text>
            <Button
              size="compact-sm"
              variant="subtle"
              leftSection={<IconRefresh size={16} stroke={1.75} />}
              loading={vm.checking}
              onClick={() => void vm.refresh()}
            >
              {t('versions.summary.check')}
            </Button>
          </div>
          <div className={classes.grid}>
            <div>
              <Text className={classes.label}>{t('versions.summary.installed')}</Text>
              <Text className={classes.value}>{vm.installed ? `v${vm.installed}` : t('versions.summary.none')}</Text>
            </div>
            <div>
              <Text className={classes.label}>{t('versions.summary.latest')}</Text>
              <Text className={classes.value}>
                {latest ? `v${latest.version}` : '—'}
                {latest?.prerelease && (
                  <Badge size="xs" variant="light" ml={6}>
                    {t('versions.prerelease')}
                  </Badge>
                )}
              </Text>
            </div>
            <div>
              <Text className={classes.label}>{t('versions.summary.platform')}</Text>
              <Text className={classes.value}>{vm.check?.platform ?? '—'}</Text>
            </div>
            <div>
              <Text className={classes.label}>{t('versions.summary.source')}</Text>
              <Text className={classes.value}>
                {vm.check?.source ? sourceName(vm.check.source) : '—'}
                {vm.check?.cached && (
                  <Badge size="xs" variant="outline" ml={6}>
                    {t('versions.summary.cached')}
                  </Badge>
                )}
              </Text>
            </div>
          </div>
          {vm.network && (
            <Text className={classes.hint}>
              {t(`versions.summary.mode.${vm.network.downloadSource}`)}
              {vm.activeSource && ` · ${t('versions.summary.next', { source: sourceName(vm.activeSource) })}`}
              {vm.network.proxy && ` · ${t('versions.summary.proxy', { proxy: vm.network.proxy })}`}
            </Text>
          )}

          {vm.checkError && renderError(vm.checkError)}

          {vm.downloading && vm.progress ? (
            <div className={classes.progress}>
              <div className={classes.progressRow}>
                <Text className={classes.progressText}>{progressLabel()}</Text>
                <Button size="compact-xs" variant="subtle" leftSection={<IconX size={14} stroke={1.75} />} onClick={vm.cancel}>
                  {t('versions.cancel')}
                </Button>
              </div>
              <Progress value={vm.percent} animated={vm.progress.phase === 'downloading'} />
              {vm.progress.source && (
                <Text className={classes.progressMeta}>
                  {t('versions.via', { source: sourceName(vm.progress.source, vm.progress.host) })}
                  {vm.progress.resumed && ` · ${t('versions.resumed')}`}
                </Text>
              )}
            </div>
          ) : (
            <div className={classes.actions}>
              {vm.check?.available && latest ? (
                <Button leftSection={<IconDownload size={18} stroke={1.75} />} onClick={() => void vm.install('')}>
                  {vm.installed
                    ? t('versions.updateTo', { version: latest.version })
                    : t('versions.downloadLatest', { version: latest.version })}
                </Button>
              ) : (
                vm.check && <Text className={classes.ok}>{t('versions.upToDate')}</Text>
              )}
              {pendingRestart && (
                <Button variant="light" leftSection={<IconRefresh size={18} stroke={1.75} />} onClick={vm.restart}>
                  {t('versions.restartToApply', { version: vm.installed })}
                </Button>
              )}
            </div>
          )}
          {vm.downloadError && renderError(vm.downloadError)}
          {!vm.downloading && vm.progress?.phase === 'done' && vm.progress.source && (
            <Text className={classes.ok}>
              {t('versions.doneVia', { version: vm.progress.version, source: sourceName(vm.progress.source, vm.progress.host) })}
            </Text>
          )}

          <div className={classes.dirRow}>
            <Text className={classes.label}>{t('versions.dataDir')}</Text>
            <Text className={classes.path}>{vm.dataDir}</Text>
            <Button size="compact-xs" variant="subtle" leftSection={<IconFolderOpen size={14} stroke={1.75} />} onClick={vm.reveal}>
              {t('versions.reveal')}
            </Button>
          </div>
        </section>

        <section className={classes.card}>
          <Text className={classes.cardTitle}>{t('versions.list.title')}</Text>
          <Text className={classes.hint}>{t('versions.list.hint')}</Text>
          {vm.releasesError && renderError(vm.releasesError)}
          <div className={classes.list}>
            {vm.releases.map((r) => {
              const isInstalled = r.version === vm.installed;
              return (
                <div key={r.tag} className={classes.release} data-installed={isInstalled || undefined}>
                  <div className={classes.releaseMain}>
                    <div className={classes.releaseTitle}>
                      <Text className={classes.releaseVersion}>v{r.version}</Text>
                      {r.prerelease ? (
                        <Badge size="xs" variant="light">{t('versions.prerelease')}</Badge>
                      ) : (
                        <Badge size="xs" variant="outline">{t('versions.stable')}</Badge>
                      )}
                      {isInstalled && <Badge size="xs">{t('versions.current')}</Badge>}
                    </div>
                    <Text className={classes.releaseMeta}>
                      {new Date(r.publishedAt).toLocaleString()} · {formatBytes(r.assetSize)}
                    </Text>
                  </div>
                  <div className={classes.releaseActions}>
                    <Button
                      size="compact-sm"
                      variant="subtle"
                      leftSection={<IconExternalLink size={14} stroke={1.75} />}
                      onClick={() => vm.openRelease(r.htmlUrl)}
                    >
                      {t('versions.notes')}
                    </Button>
                    <Button
                      size="compact-sm"
                      variant="light"
                      disabled={isInstalled || vm.downloading}
                      onClick={() => void vm.install(r.version)}
                    >
                      {isInstalled ? t('versions.installed') : t('versions.install')}
                    </Button>
                  </div>
                </div>
              );
            })}
          </div>
        </section>
      </div>
    </div>
  );
};

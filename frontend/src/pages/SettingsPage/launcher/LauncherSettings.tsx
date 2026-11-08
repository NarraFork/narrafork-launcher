import { ActionIcon, Button, Loader, NumberInput, Select, Switch, Text, TextInput, Tooltip } from '@mantine/core';
import {
  IconAlertTriangle,
  IconCircleCheck,
  IconCircleMinus,
  IconCircleX,
  IconFolderOpen,
  IconNetwork,
  IconRestore,
  IconTrash,
  IconX,
} from '@tabler/icons-react';
import clsx from 'clsx';
import { useT } from '@/i18n';
import type { DownloadProxyMode, DownloadSource, NarraforkProxyMode } from '@/services/launcher';
import { useLauncherSettings } from './useLauncherSettings';
import classes from './LauncherSettings.module.css';
import shared from '../SettingsPage.module.css';

// 镜像地址示例里拼接的原始 GitHub 链接（URL，不是文案）。
const MIRROR_EXAMPLE_PATH = 'https://api.github.com/repos/NarraFork/NarraFork/releases/latest';

// 启动器设置：NarraFork 服务参数 / 打开方式 / 更新通道 / 网络 / 内置浏览器数据。
// 文案 namespace：settings.launcher.*
export const LauncherSettings = () => {
  const t = useT();
  const vm = useLauncherSettings();
  const cfg = vm.config;

  if (!cfg) {
    return <Text className={shared.sectionHint}>{vm.error?.message ?? t('common.loading')}</Text>;
  }

  const sourceName = (src: 'github' | 'mirror') =>
    t(`settings.launcher.network.sourceName.${src}`, { host: vm.mirrorHost });
  const net = vm.network;

  return (
    <>
      <div className={shared.section}>
        <div className={shared.sectionHeader}>
          <Text className={shared.sectionTitle}>{t('settings.launcher.server.title')}</Text>
          <Text className={shared.sectionHint}>{t('settings.launcher.server.hint')}</Text>
        </div>
        <div className={clsx(classes.card, classes.grid)} key={`server-${vm.inputsVersion}`}>
          <NumberInput
            label={t('settings.launcher.server.port')}
            placeholder={t('settings.launcher.server.portPlaceholder')}
            defaultValue={cfg.port || ''}
            min={1}
            max={65535}
            allowDecimal={false}
            onBlur={(e) => {
              const v = Number(e.currentTarget.value) || 0;
              if (v !== cfg.port) void vm.update({ port: v });
            }}
          />
          <TextInput
            label={t('settings.launcher.server.host')}
            placeholder={t('settings.launcher.server.hostPlaceholder')}
            defaultValue={cfg.host}
            onBlur={(e) => {
              const v = e.currentTarget.value.trim();
              if (v !== cfg.host) void vm.update({ host: v });
            }}
          />
        </div>
        <div className={classes.card}>
          <TextInput
            label={t('settings.launcher.server.binary')}
            description={t('settings.launcher.server.binaryHint')}
            placeholder={t('settings.launcher.server.binaryPlaceholder')}
            value={cfg.binaryPath}
            readOnly
            rightSectionWidth={64}
            rightSection={
              <div className={classes.inputActions}>
                {cfg.binaryPath && (
                  <Tooltip label={t('settings.launcher.server.binaryClear')}>
                    <ActionIcon size="sm" variant="subtle" aria-label={t('settings.launcher.server.binaryClear')} onClick={() => void vm.update({ binaryPath: '' })}>
                      <IconX size={16} stroke={1.75} />
                    </ActionIcon>
                  </Tooltip>
                )}
                <Tooltip label={t('settings.launcher.server.binaryBrowse')}>
                  <ActionIcon
                    size="sm"
                    variant="subtle"
                    aria-label={t('settings.launcher.server.binaryBrowse')}
                    onClick={() => void vm.browseBinary(t('settings.launcher.server.binaryBrowse'), t('settings.launcher.server.binaryFilter'))}
                  >
                    <IconFolderOpen size={16} stroke={1.75} />
                  </ActionIcon>
                </Tooltip>
              </div>
            }
          />
        </div>
      </div>

      <div className={shared.section}>
        <div className={shared.sectionHeader}>
          <Text className={shared.sectionTitle}>{t('settings.launcher.launch.title')}</Text>
        </div>
        <div className={classes.card}>
          <Switch
            label={t('settings.launcher.launch.autoStart')}
            description={t('settings.launcher.launch.autoStartHint')}
            checked={cfg.autoStart}
            onChange={(e) => void vm.update({ autoStart: e.currentTarget.checked })}
          />
          <Switch
            label={t('settings.launcher.launch.openOnReady')}
            checked={cfg.openOnReady}
            onChange={(e) => void vm.update({ openOnReady: e.currentTarget.checked })}
          />
          <div className={classes.grid}>
            <Select
              label={t('settings.launcher.launch.mode')}
              allowDeselect={false}
              value={cfg.openMode}
              data={[
                { value: 'inline', label: t('settings.launcher.launch.modeInline') },
                { value: 'external', label: t('settings.launcher.launch.modeExternal') },
              ]}
              onChange={(v) => v && void vm.update({ openMode: v as 'inline' | 'external' })}
            />
            <Select
              label={t('settings.launcher.launch.closeAction')}
              allowDeselect={false}
              value={cfg.closeAction}
              data={[
                { value: 'minimize', label: t('settings.launcher.launch.closeMinimize') },
                { value: 'quit', label: t('settings.launcher.launch.closeQuit') },
              ]}
              onChange={(v) => v && void vm.update({ closeAction: v as 'minimize' | 'quit' })}
            />
          </div>
        </div>
      </div>

      <div className={shared.section}>
        <div className={shared.sectionHeader}>
          <Text className={shared.sectionTitle}>{t('settings.launcher.updates.title')}</Text>
        </div>
        <div className={classes.card}>
          <Switch
            label={t('settings.launcher.updates.autoUpdate')}
            description={t('settings.launcher.updates.autoUpdateHint')}
            checked={cfg.autoUpdate}
            onChange={(e) => void vm.update({ autoUpdate: e.currentTarget.checked })}
          />
          <Select
            label={t('settings.launcher.updates.channel')}
            description={t('settings.launcher.updates.channelHint')}
            allowDeselect={false}
            value={cfg.channel}
            data={[
              { value: 'stable', label: t('settings.launcher.updates.stable') },
              { value: 'beta', label: t('settings.launcher.updates.beta') },
            ]}
            onChange={(v) => v && void vm.update({ channel: v as 'stable' | 'beta' })}
          />
        </div>
      </div>

      <div className={shared.section}>
        <div className={shared.sectionHeader}>
          <Text className={shared.sectionTitle}>{t('settings.launcher.network.title')}</Text>
          <Text className={shared.sectionHint}>{t('settings.launcher.network.hint')}</Text>
        </div>
        <div className={classes.card} key={`network-${vm.inputsVersion}`}>
          <Select
            label={t('settings.launcher.network.source.label')}
            description={t('settings.launcher.network.source.hint')}
            allowDeselect={false}
            value={cfg.downloadSource}
            data={[
              { value: 'auto', label: t('settings.launcher.network.source.auto') },
              { value: 'github', label: t('settings.launcher.network.source.github') },
              { value: 'mirror', label: t('settings.launcher.network.source.mirror') },
            ]}
            onChange={(v) => v && void vm.update({ downloadSource: v as DownloadSource })}
          />
          {vm.activeSource && (
            <Text className={classes.rowHint}>
              {cfg.downloadSource === 'auto' && vm.activeSource === 'mirror'
                ? t('settings.launcher.network.source.activeAuto', { source: sourceName('mirror') })
                : t('settings.launcher.network.source.active', { source: sourceName(vm.activeSource) })}
              {' · '}
              {vm.lastSource
                ? t('settings.launcher.network.source.lastUsed', { source: sourceName(vm.lastSource) })
                : t('settings.launcher.network.source.none')}
            </Text>
          )}
          <TextInput
            label={t('settings.launcher.network.source.mirrorUrl')}
            description={t('settings.launcher.network.source.mirrorUrlHint', { example: cfg.mirrorUrl + MIRROR_EXAMPLE_PATH })}
            defaultValue={cfg.mirrorUrl}
            disabled={cfg.downloadSource === 'github'}
            onBlur={(e) => {
              const v = e.currentTarget.value.trim();
              if (v !== cfg.mirrorUrl) void vm.update({ mirrorUrl: v });
            }}
          />

          <div className={classes.divider} />

          <Select
            label={t('settings.launcher.network.proxy.label')}
            description={t('settings.launcher.network.proxy.hint')}
            allowDeselect={false}
            value={cfg.downloadProxyMode}
            data={[
              { value: 'system', label: t('settings.launcher.network.proxy.system') },
              { value: 'direct', label: t('settings.launcher.network.proxy.direct') },
              { value: 'custom', label: t('settings.launcher.network.proxy.custom') },
            ]}
            onChange={(v) => v && void vm.update({ downloadProxyMode: v as DownloadProxyMode })}
          />
          {cfg.downloadProxyMode === 'system' && net && (
            <Text className={classes.rowHint}>
              {net.systemProxy
                ? t('settings.launcher.network.proxy.systemDetected', {
                    proxy: net.systemProxy,
                    origin: t(`settings.launcher.network.proxy.origin.${net.systemProxyOrigin || 'env'}`),
                  })
                : t('settings.launcher.network.proxy.systemNone')}
            </Text>
          )}
          {cfg.downloadProxyMode === 'custom' && (
            <>
              <TextInput
                label={t('settings.launcher.network.proxy.url')}
                description={t('settings.launcher.network.proxy.urlHint')}
                placeholder={t('settings.launcher.network.proxy.urlPlaceholder')}
                defaultValue={cfg.downloadProxyUrl}
                onBlur={(e) => {
                  const v = e.currentTarget.value.trim();
                  if (v !== cfg.downloadProxyUrl) void vm.update({ downloadProxyUrl: v });
                }}
              />
              {!cfg.downloadProxyUrl && <Text className={classes.warn}>{t('settings.launcher.network.proxy.urlMissing')}</Text>}
            </>
          )}

          <div className={classes.divider} />

          <Select
            label={t('settings.launcher.network.narrafork.label')}
            allowDeselect={false}
            value={cfg.narraforkProxyMode}
            data={[
              { value: 'inherit', label: t('settings.launcher.network.narrafork.inherit') },
              { value: 'none', label: t('settings.launcher.network.narrafork.none') },
              { value: 'system', label: t('settings.launcher.network.narrafork.system') },
              { value: 'custom', label: t('settings.launcher.network.narrafork.custom') },
              { value: 'download', label: t('settings.launcher.network.narrafork.download') },
            ]}
            onChange={(v) => v && void vm.update({ narraforkProxyMode: v as NarraforkProxyMode })}
          />
          {cfg.narraforkProxyMode === 'custom' && (
            <>
              <TextInput
                label={t('settings.launcher.network.narrafork.url')}
                description={t('settings.launcher.network.proxy.urlHint')}
                placeholder={t('settings.launcher.network.proxy.urlPlaceholder')}
                defaultValue={cfg.narraforkProxyUrl}
                onBlur={(e) => {
                  const v = e.currentTarget.value.trim();
                  if (v !== cfg.narraforkProxyUrl) void vm.update({ narraforkProxyUrl: v });
                }}
              />
              {!cfg.narraforkProxyUrl && <Text className={classes.warn}>{t('settings.launcher.network.proxy.urlMissing')}</Text>}
            </>
          )}
          <div className={classes.notice}>
            <IconAlertTriangle size={16} stroke={1.75} className={classes.noticeIcon} />
            <Text className={classes.rowHint}>{t('settings.launcher.network.narrafork.hint')}</Text>
          </div>
        </div>

        <div className={classes.card}>
          <div className={classes.row}>
            <div>
              <Text className={classes.rowTitle}>{t('settings.launcher.network.test.title')}</Text>
              <Text className={classes.rowHint}>{t('settings.launcher.network.test.hint')}</Text>
            </div>
            <Button
              variant="light"
              leftSection={<IconNetwork size={18} stroke={1.75} />}
              loading={vm.testing}
              onClick={() => void vm.testNetwork()}
            >
              {t('settings.launcher.network.test.run')}
            </Button>
          </div>
          {vm.testRows && (
            <div className={classes.testList}>
              {vm.testRows.map((row) => (
                <div key={row.id} className={classes.testRow} data-state={row.state}>
                  {row.state === 'ok' && <IconCircleCheck size={18} stroke={1.75} className={classes.testIcon} />}
                  {row.state === 'fail' && <IconCircleX size={18} stroke={1.75} className={classes.testIcon} />}
                  {row.state === 'skip' && <IconCircleMinus size={18} stroke={1.75} className={classes.testIcon} />}
                  <Text className={classes.testName}>{t(`settings.launcher.network.test.targets.${row.id}`)}</Text>
                  <Tooltip label={row.detail} disabled={!row.detail} multiline maw={420}>
                    <Text className={classes.testResult}>
                      {t(row.key, { ...row.vars, reason: row.reasonKey ? t(row.reasonKey) : '' })}
                    </Text>
                  </Tooltip>
                </div>
              ))}
            </div>
          )}
          {vm.testing && !vm.testRows && <Loader size="sm" />}
          {vm.testError && <Text className={classes.error}>{vm.testError.message}</Text>}
        </div>
      </div>

      <div className={shared.section}>
        <div className={shared.sectionHeader}>
          <Text className={shared.sectionTitle}>{t('settings.launcher.data.title')}</Text>
          <Text className={shared.sectionHint}>{t('settings.launcher.data.hint')}</Text>
        </div>
        <div className={classes.card}>
          <div className={classes.row}>
            <div>
              <Text className={classes.rowTitle}>{t('settings.launcher.data.clear')}</Text>
              <Text className={classes.rowHint}>{t('settings.launcher.data.clearHint')}</Text>
            </div>
            <Button
              variant="light"
              className={classes.danger}
              leftSection={<IconTrash size={18} stroke={1.75} />}
              loading={vm.clearing}
              onClick={() => void vm.clearSiteData()}
            >
              {t('settings.launcher.data.clear')}
            </Button>
          </div>
          <div className={classes.row}>
            <div>
              <Text className={classes.rowTitle}>{t('settings.launcher.data.reset')}</Text>
              <Text className={classes.rowHint}>{t('settings.launcher.data.resetHint')}</Text>
            </div>
            <Button
              variant="subtle"
              className={classes.danger}
              leftSection={<IconRestore size={18} stroke={1.75} />}
              disabled={vm.clearing}
              onClick={() =>
                void vm.resetWebview({
                  title: t('settings.launcher.data.reset'),
                  message: t('settings.launcher.data.resetConfirm'),
                  okLabel: t('settings.launcher.data.resetOk'),
                  cancelLabel: t('common.cancel'),
                })
              }
            >
              {t('settings.launcher.data.reset')}
            </Button>
          </div>
          {vm.clearMessageKey && <Text className={classes.ok}>{t(vm.clearMessageKey)}</Text>}
        </div>
      </div>

      {vm.error && (
        <Text className={classes.error}>
          {vm.errorKey ? t(vm.errorKey) : vm.error.message}
          {vm.errorKey && ` ${t('settings.launcher.errors.detail', { message: vm.error.message })}`}
        </Text>
      )}
      {vm.saveState === 'saved' && !vm.error && <Text className={classes.saved}>{t('settings.launcher.saved')}</Text>}
    </>
  );
};

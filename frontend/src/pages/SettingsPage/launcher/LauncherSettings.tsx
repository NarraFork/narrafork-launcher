import { ActionIcon, Button, NumberInput, Select, Switch, Text, TextInput, Tooltip } from '@mantine/core';
import { IconFolderOpen, IconTrash, IconRestore, IconX } from '@tabler/icons-react';
import clsx from 'clsx';
import { useT } from '@/i18n';
import { useLauncherSettings } from './useLauncherSettings';
import classes from './LauncherSettings.module.css';
import shared from '../SettingsPage.module.css';

// 启动器设置：NarraFork 服务参数 / 打开方式 / 更新通道 / 内置浏览器数据。
// 文案 namespace：settings.launcher.*
export const LauncherSettings = () => {
  const t = useT();
  const vm = useLauncherSettings();
  const cfg = vm.config;

  if (!cfg) {
    return <Text className={shared.sectionHint}>{vm.error ?? t('common.loading')}</Text>;
  }

  return (
    <>
      <div className={shared.section}>
        <div className={shared.sectionHeader}>
          <Text className={shared.sectionTitle}>{t('settings.launcher.server.title')}</Text>
          <Text className={shared.sectionHint}>{t('settings.launcher.server.hint')}</Text>
        </div>
        <div className={clsx(classes.card, classes.grid)}>
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

      {vm.error && <Text className={classes.error}>{vm.error}</Text>}
      {vm.saveState === 'saved' && !vm.error && <Text className={classes.saved}>{t('settings.launcher.saved')}</Text>}
    </>
  );
};

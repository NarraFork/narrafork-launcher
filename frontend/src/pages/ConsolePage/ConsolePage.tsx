import { ActionIcon, Badge, Button, Text, Tooltip } from '@mantine/core';
import {
  IconArrowDown,
  IconBrowser,
  IconExternalLink,
  IconPlayerPlay,
  IconPlayerStop,
  IconRefresh,
  IconTrash,
} from '@tabler/icons-react';
import { useT } from '@/i18n';
import { formatDuration } from '@/shared/format';
import { useConsolePage } from './useConsolePage';
import classes from './ConsolePage.module.css';

// 控制台：顶部状态 + 操作，下方为 NarraFork 服务输出（对应旧启动器的"终端"页）。
export const ConsolePage = () => {
  const t = useT();
  const vm = useConsolePage();
  const { status } = vm;

  return (
    <div className={classes.root}>
      <div className={classes.header}>
        <div className={classes.statusGroup}>
          <span className={classes.dot} data-state={status.state} aria-hidden />
          <Text className={classes.title}>{t('console.title')}</Text>
          <Badge variant="light" className={classes.badge} data-state={status.state}>
            {t(vm.stateKey)}
          </Badge>
          {status.preparing && <Text className={classes.meta}>{t('console.preparing')}</Text>}
          {(status.version || status.installed) && (
            <Text className={classes.meta}>v{status.version || status.installed}</Text>
          )}
          {vm.uptime !== null && (
            <Text className={classes.meta}>{t('console.uptime', { time: formatDuration(vm.uptime) })}</Text>
          )}
          {vm.running && status.url && <Text className={classes.url}>{status.url}</Text>}
        </div>
        <div className={classes.actions}>
          {vm.running ? (
            <>
              <Button size="compact-sm" variant="light" leftSection={<IconBrowser size={16} stroke={1.75} />} onClick={() => void vm.open()}>
                {t('console.actions.open')}
              </Button>
              <Tooltip label={t('console.actions.openExternal')}>
                <ActionIcon variant="subtle" aria-label={t('console.actions.openExternal')} onClick={() => void vm.openExternal()}>
                  <IconExternalLink size={18} stroke={1.75} />
                </ActionIcon>
              </Tooltip>
              <Tooltip label={t('console.actions.restart')}>
                <ActionIcon variant="subtle" aria-label={t('console.actions.restart')} onClick={() => void vm.restart()}>
                  <IconRefresh size={18} stroke={1.75} />
                </ActionIcon>
              </Tooltip>
              <Button
                size="compact-sm"
                variant="light"
                className={classes.danger}
                leftSection={<IconPlayerStop size={16} stroke={1.75} />}
                loading={status.state === 'stopping'}
                onClick={() => void vm.stop()}
              >
                {t('console.actions.stop')}
              </Button>
            </>
          ) : status.state === 'starting' ? (
            <Button
              size="compact-sm"
              variant="light"
              className={classes.danger}
              leftSection={<IconPlayerStop size={16} stroke={1.75} />}
              onClick={() => void vm.stop()}
            >
              {t('console.actions.cancel')}
            </Button>
          ) : (
            <Button
              size="compact-sm"
              leftSection={<IconPlayerPlay size={16} stroke={1.75} />}
              loading={vm.transitioning || vm.busy}
              onClick={() => void vm.start()}
            >
              {status.installed || status.binaryPath ? t('console.actions.start') : t('console.actions.downloadAndStart')}
            </Button>
          )}
        </div>
      </div>

      {vm.actionError && <Text className={classes.error}>{vm.actionError}</Text>}

      <div className={classes.terminalWrap}>
        <div className={classes.terminalToolbar}>
          <Text className={classes.toolbarLabel}>{t('console.logs')}</Text>
          <div className={classes.toolbarActions}>
            {!vm.follow && (
              <Tooltip label={t('console.jumpToEnd')}>
                <ActionIcon size="sm" variant="subtle" aria-label={t('console.jumpToEnd')} onClick={vm.jumpToEnd}>
                  <IconArrowDown size={16} stroke={1.75} />
                </ActionIcon>
              </Tooltip>
            )}
            <Tooltip label={t('console.clear')}>
              <ActionIcon size="sm" variant="subtle" aria-label={t('console.clear')} onClick={() => void vm.clear()}>
                <IconTrash size={16} stroke={1.75} />
              </ActionIcon>
            </Tooltip>
          </div>
        </div>
        <div className={classes.terminal} ref={vm.scrollRef} onScroll={vm.onScroll}>
          {vm.lines.length === 0 ? (
            <Text className={classes.empty}>{t('console.empty')}</Text>
          ) : (
            vm.lines.map((line) => (
              <div key={line.seq} className={classes.line}>
                {line.spans.map((s, i) => (
                  <span
                    key={i}
                    className={classes[`tone_${s.tone ?? line.tone}`]}
                    data-bold={s.bold || undefined}
                    data-dim={s.dim || undefined}
                    style={s.rgb ? { color: s.rgb } : undefined}
                  >
                    {s.text}
                  </span>
                ))}
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
};

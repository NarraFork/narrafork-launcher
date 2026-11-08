import { memo } from 'react';
import { ActionIcon, Badge, Button, SegmentedControl, Text, Tooltip } from '@mantine/core';
import {
  IconArrowDown,
  IconBrightnessAuto,
  IconBrowser,
  IconChevronRight,
  IconExternalLink,
  IconMoon,
  IconPlayerPlay,
  IconPlayerStop,
  IconRefresh,
  IconSun,
  IconTrash,
  type Icon,
} from '@tabler/icons-react';
import { useT } from '@/i18n';
import { formatDuration } from '@/shared/format';
import type { Span, TermColor } from './ansi';
import { useConsolePage, type ConsoleLine, type TerminalScheme } from './useConsolePage';
import classes from './ConsolePage.module.css';

const SCHEME_ICONS: Record<TerminalScheme, Icon> = {
  auto: IconBrightnessAuto,
  light: IconSun,
  dark: IconMoon,
};

// 终端色 → class（16 色 / 默认前景背景走 --fd-term-*）或内联 rgb()（256 色 / 真彩色）
const colorClass = (c: TermColor | undefined, layer: 'fg' | 'bg'): string | undefined => {
  if (c === undefined || (typeof c === 'string' && c.startsWith('rgb('))) return undefined;
  return classes[`${layer}_${c}`];
};

const spanStyle = (s: Span) => {
  const fg = typeof s.fg === 'string' && s.fg.startsWith('rgb(') ? s.fg : undefined;
  const bg = typeof s.bg === 'string' && s.bg.startsWith('rgb(') ? s.bg : undefined;
  return fg || bg ? { color: fg, backgroundColor: bg } : undefined;
};

const spanClassName = (s: Span): string | undefined =>
  [s.role && classes[s.role], colorClass(s.fg, 'fg'), colorClass(s.bg, 'bg')].filter(Boolean).join(' ') ||
  undefined;

// 单行：行对象按 seq 缓存、引用稳定，memo 后新批次到达时旧行不重渲染
const TerminalLine = memo(({ line }: { line: ConsoleLine }) => (
  <div className={classes.line} data-kind={line.kind} data-level={line.level}>
    {line.kind === 'system' && <IconChevronRight className={classes.systemMarker} size={12} stroke={2.25} aria-hidden />}
    {line.spans.map((s, i) => (
      <span
        key={i}
        className={spanClassName(s)}
        style={spanStyle(s)}
        data-bold={s.bold || undefined}
        data-dim={s.dim || undefined}
        data-italic={s.italic || undefined}
        data-underline={s.underline}
        data-strike={s.strike || undefined}
      >
        {s.text}
      </span>
    ))}
  </div>
));

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
            <SegmentedControl
              size="xs"
              aria-label={t('console.terminalScheme.label')}
              classNames={{ root: classes.schemeControl, label: classes.schemeLabel }}
              value={vm.terminalScheme}
              onChange={vm.setTerminalScheme}
              data={vm.terminalSchemes.map(({ value, labelKey }) => {
                const SchemeIcon = SCHEME_ICONS[value];
                return {
                  value,
                  label: (
                    <span className={classes.schemeOption}>
                      <SchemeIcon size={14} stroke={1.75} />
                      <span>{t(labelKey)}</span>
                    </span>
                  ),
                };
              })}
            />
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
        <div
          className={classes.terminal}
          data-term-scheme={vm.terminalScheme}
          ref={vm.scrollRef}
          onScroll={vm.onScroll}
        >
          {vm.lines.length === 0 ? (
            <Text className={classes.empty}>{t('console.empty')}</Text>
          ) : (
            vm.lines.map((line) => <TerminalLine key={line.seq} line={line} />)
          )}
        </div>
      </div>
    </div>
  );
};

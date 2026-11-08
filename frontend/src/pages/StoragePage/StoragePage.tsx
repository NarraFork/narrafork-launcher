import { Badge, Button, Loader, Text, Tooltip } from '@mantine/core';
import { DonutChart } from '@mantine/charts';
import {
  IconAlertTriangle,
  IconDatabaseExport,
  IconFolderOpen,
  IconFolderCog,
  IconLock,
  IconPlayerStop,
  IconRefresh,
  IconRestore,
  IconTrash,
} from '@tabler/icons-react';
import { useT } from '@/i18n';
import { NativeDialogs } from '@/services';
import type { StorageCategory, StorageRisk } from '@/services/narrastore';
import { formatBytes } from '@/shared/format';
import { FOUNDATION_CSS_VARS } from '@/styles/themes';
import { useStoragePage, type StorageFeedback } from './useStoragePage';
import classes from './StoragePage.module.css';

type T = ReturnType<typeof useT>;
type Vm = ReturnType<typeof useStoragePage>;

// 每个风险等级的基色；同一等级内按序号与背景色混合区分深浅。全部取自主题 CSS 变量。
const RISK_BASE: Record<StorageRisk, string> = {
  safe: FOUNDATION_CSS_VARS.status.success,
  caution: FOUNDATION_CSS_VARS.status.warning,
  danger: FOUNDATION_CSS_VARS.status.danger,
};
const SHADES = [100, 72, 50, 34];
const shade = (base: string, i: number): string => {
  const pct = SHADES[i % SHADES.length] ?? 100;
  return pct === 100 ? base : `color-mix(in srgb, ${base} ${pct}%, ${FOUNDATION_CSS_VARS.bg.surface})`;
};
// 只统计不删除的分类用中性色。
const NEUTRAL = [FOUNDATION_CSS_VARS.text.muted, FOUNDATION_CSS_VARS.text.secondary];
const neutral = (i: number): string => NEUTRAL[i % NEUTRAL.length] ?? FOUNDATION_CSS_VARS.text.muted;

const colorMap = (cats: StorageCategory[]): Map<string, string> => {
  const map = new Map<string, string>();
  const counter: Record<string, number> = {};
  for (const c of cats) {
    const group = c.deletable ? c.risk : 'neutral';
    const i = counter[group] ?? 0;
    counter[group] = i + 1;
    map.set(c.id, c.deletable ? shade(RISK_BASE[c.risk], i) : neutral(i));
  }
  return map;
};

const MAX_CONFIRM_PATHS = 6;

const formatTime = (ms: number): string => (ms > 0 ? new Date(ms).toLocaleString() : '');

// 存储：接管 NarraFork 数据目录 —— 路径 / 切换 / 占用统计 / 分类删除。
export const StoragePage = () => {
  const t = useT();
  const vm = useStoragePage();
  const colors = colorMap(vm.chartCategories);

  const handleChangeHome = async (): Promise<void> => {
    if (vm.busy || !vm.home) return;
    const picked = await NativeDialogs.openDirectory({
      title: t('storage.home.pickTitle'),
      buttonText: t('storage.home.pickButton'),
      directory: vm.home.path,
    });
    if (!picked) return;
    const check = await vm.checkHome(picked);
    if (!check.valid) {
      await NativeDialogs.warning(t('storage.home.invalidTitle'), t(`storage.home.invalid.${check.error || 'notCreatable'}`));
      return;
    }
    if (check.isDefault) {
      if (vm.home.custom) await handleResetHome();
      return;
    }
    const intro = check.looksLikeHome
      ? t('storage.home.confirmExisting', { path: check.path })
      : check.empty
        ? t('storage.home.confirmEmpty', { path: check.path })
        : t('storage.home.confirmNonEmpty', { path: check.path });
    const lines = [intro, t('storage.home.confirmNoMigration', { current: vm.home.path })];
    if (vm.running) lines.push(t('storage.home.confirmRunning'));
    const ok = await NativeDialogs.confirm({
      title: t('storage.home.confirmTitle'),
      message: lines.join('\n\n'),
      okLabel: t('storage.home.confirmOk'),
      cancelLabel: t('storage.home.cancel'),
    });
    if (ok) await vm.changeHome(check.path);
  };

  const handleResetHome = async (): Promise<void> => {
    if (vm.busy || !vm.home) return;
    const lines = [t('storage.home.resetMessage', { path: vm.home.defaultPath })];
    if (vm.running) lines.push(t('storage.home.confirmRunning'));
    const ok = await NativeDialogs.confirm({
      title: t('storage.home.resetTitle'),
      message: lines.join('\n\n'),
      okLabel: t('storage.home.resetOk'),
      cancelLabel: t('storage.home.cancel'),
    });
    if (ok) await vm.resetHome();
  };

  const handleDelete = async (c: StorageCategory): Promise<void> => {
    if (vm.deleteAvailability(c).disabled) return;
    const label = t(c.labelKey);
    const description = t(c.descriptionKey);
    const size = formatBytes(c.bytes);
    const shown = c.paths.slice(0, MAX_CONFIRM_PATHS);
    const more = c.paths.length - shown.length + c.morePaths;
    const paths = more > 0 ? `${shown.join(', ')} ${t('storage.list.morePaths', { count: more })}` : shown.join(', ');
    const summary = t('storage.confirm.summary', { size, files: c.files, paths });
    const detail = t(`storage.confirm.${c.risk}`, { description });
    const ok = await NativeDialogs.confirm({
      title: t('storage.confirm.title', { label }),
      message: `${summary}\n\n${detail}`,
      okLabel: c.risk === 'danger' ? t('storage.confirm.dangerOk') : t('storage.confirm.ok'),
      cancelLabel: t('storage.confirm.cancel'),
    });
    if (!ok) return;
    // 危险分类再确认一次，才把 confirmDanger=true 传给后端。
    if (c.risk === 'danger') {
      const again = await NativeDialogs.confirm({
        title: t('storage.confirm.againTitle'),
        message: t('storage.confirm.againMessage', { label, size }),
        okLabel: t('storage.confirm.againOk'),
        cancelLabel: t('storage.confirm.cancel'),
      });
      if (!again) return;
    }
    await vm.deleteCategory(c, c.risk === 'danger');
  };

  const handleBackup = async (): Promise<void> => {
    if (vm.busy || vm.running) return;
    const dir = await NativeDialogs.openDirectory({
      title: t('storage.backup.pickTitle'),
      buttonText: t('storage.backup.pickButton'),
    });
    if (!dir) return;
    try {
      const r = await vm.backupDatabase(dir);
      await NativeDialogs.info(
        t('storage.backup.doneTitle'),
        t('storage.backup.done', { files: r.files.join(', '), size: formatBytes(r.bytes), dir: r.dir }),
      );
    } catch (e) {
      await NativeDialogs.error(t('storage.backup.failedTitle'), e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <div className={classes.root}>
      <div className={classes.body}>
        <HomeCard vm={vm} t={t} onChange={handleChangeHome} onReset={handleResetHome} />

        {vm.running && (
          <div className={classes.runningBanner}>
            <IconPlayerStop size={18} stroke={1.75} className={classes.runningIcon} />
            <Text className={classes.runningText}>
              {vm.externalPid
                ? t('storage.running.external', { pid: vm.externalPid })
                : t('storage.running.managed', { pid: vm.runningPid || '—' })}
            </Text>
          </div>
        )}

        <SummaryCard vm={vm} t={t} colors={colors} />

        <section className={classes.card}>
          <div className={classes.cardHeader}>
            <Text className={classes.cardTitle}>{t('storage.list.title')}</Text>
            {vm.emptyCount > 0 && (
              <Button size="compact-sm" variant="subtle" onClick={() => vm.setShowEmpty(!vm.showEmpty)}>
                {vm.showEmpty ? t('storage.list.hideEmpty') : t('storage.list.showEmpty', { count: vm.emptyCount })}
              </Button>
            )}
          </div>
          <Text className={classes.hint}>{t('storage.list.hint')}</Text>
          <FeedbackLine feedback={vm.feedback} t={t} />
          <div className={classes.list}>
            {vm.categories.map((c) => (
              <CategoryRow
                key={c.id}
                category={c}
                color={colors.get(c.id)}
                vm={vm}
                t={t}
                onDelete={handleDelete}
                onBackup={handleBackup}
              />
            ))}
          </div>
        </section>
      </div>
    </div>
  );
};

interface HomeCardProps {
  vm: Vm;
  t: T;
  onChange: () => void;
  onReset: () => void;
}

const HomeCard = ({ vm, t, onChange, onReset }: HomeCardProps) => (
  <section className={classes.card}>
    <div className={classes.cardHeader}>
      <Text className={classes.cardTitle}>{t('storage.home.title')}</Text>
      {vm.home && (
        <Badge size="sm" variant={vm.home.custom ? 'filled' : 'light'}>
          {vm.home.custom ? t('storage.home.custom') : t('storage.home.default')}
        </Badge>
      )}
    </div>
    <Text className={classes.path}>{vm.home?.path ?? '…'}</Text>
    {vm.home?.custom && (
      <div className={classes.dirRow}>
        <Text className={classes.label}>{t('storage.home.defaultPath')}</Text>
        <Text className={classes.pathSmall}>{vm.home.defaultPath}</Text>
      </div>
    )}
    {vm.home && !vm.home.exists && <Text className={classes.hint}>{t('storage.home.notExists')}</Text>}
    {vm.home?.defaultFromEnv && <Text className={classes.hint}>{t('storage.home.fromEnv')}</Text>}
    <div className={classes.actions}>
      <Button variant="light" leftSection={<IconFolderOpen size={18} stroke={1.75} />} onClick={vm.revealHome}>
        {t('storage.home.reveal')}
      </Button>
      <Button
        variant="default"
        leftSection={<IconFolderCog size={18} stroke={1.75} />}
        disabled={vm.busy || !vm.home}
        onClick={() => void onChange()}
      >
        {t('storage.home.change')}
      </Button>
      <Button
        variant="default"
        leftSection={<IconRestore size={18} stroke={1.75} />}
        disabled={vm.busy || !vm.home?.custom}
        onClick={() => void onReset()}
      >
        {t('storage.home.reset')}
      </Button>
    </div>
    <Text className={classes.hint}>{t('storage.home.hint')}</Text>
  </section>
);

interface SummaryCardProps {
  vm: Vm;
  t: T;
  colors: Map<string, string>;
}

const SummaryCard = ({ vm, t, colors }: SummaryCardProps) => {
  const donutData = vm.chartCategories.map((c) => ({
    name: t(c.labelKey),
    value: c.bytes,
    color: colors.get(c.id) ?? FOUNDATION_CSS_VARS.accent,
  }));
  return (
    <section className={classes.card}>
      <div className={classes.cardHeader}>
        <Text className={classes.cardTitle}>{t('storage.summary.title')}</Text>
        {vm.scanning ? (
          <Button size="compact-sm" variant="subtle" leftSection={<Loader size={12} />} onClick={vm.cancelScan}>
            {t('storage.summary.cancelScan')}
          </Button>
        ) : (
          <Button
            size="compact-sm"
            variant="subtle"
            leftSection={<IconRefresh size={16} stroke={1.75} />}
            disabled={vm.busy}
            onClick={() => void vm.rescan()}
          >
            {t('storage.summary.rescan')}
          </Button>
        )}
      </div>
      <div className={classes.summary}>
        <div className={classes.chartHost}>
          {donutData.length > 0 ? (
            <DonutChart
              data={donutData}
              size={180}
              thickness={28}
              paddingAngle={1}
              strokeWidth={0}
              withTooltip
              tooltipDataSource="segment"
              valueFormatter={formatBytes}
              chartLabel={formatBytes(vm.scan?.totalBytes ?? 0)}
            />
          ) : (
            <div className={classes.chartEmpty}>
              {vm.scanning ? <Loader size="sm" /> : t('storage.summary.chartEmpty')}
            </div>
          )}
        </div>
        <div className={classes.stats}>
          <div>
            <Text className={classes.label}>{t('storage.summary.total')}</Text>
            <Text className={classes.bigValue}>{vm.scan ? formatBytes(vm.scan.totalBytes) : '—'}</Text>
          </div>
          <div>
            <Text className={classes.label}>{t('storage.summary.files')}</Text>
            <Text className={classes.value}>{vm.scan ? vm.scan.totalFiles.toLocaleString() : '—'}</Text>
          </div>
          <div>
            <Text className={classes.label}>{t('storage.summary.scannedAt')}</Text>
            <Text className={classes.value}>
              {vm.scanning
                ? t('storage.summary.scanning')
                : vm.scan
                  ? formatTime(vm.scan.scannedAt)
                  : t('storage.summary.never')}
            </Text>
          </div>
          <div className={classes.legend}>
            {vm.chartCategories.slice(0, 8).map((c) => (
              <div key={c.id} className={classes.legendItem}>
                <span className={classes.swatch} style={{ backgroundColor: colors.get(c.id) }} />
                <Text className={classes.legendLabel}>{t(c.labelKey)}</Text>
                <Text className={classes.legendValue}>{formatBytes(c.bytes)}</Text>
              </div>
            ))}
          </div>
        </div>
      </div>
      {vm.scan && !vm.scan.exists && <Text className={classes.hint}>{t('storage.summary.missing')}</Text>}
      {vm.scan?.truncated && <Text className={classes.warn}>{t('storage.summary.truncated')}</Text>}
      {vm.scan && vm.scan.skipped > 0 && (
        <Text className={classes.hint}>{t('storage.summary.skipped', { count: vm.scan.skipped })}</Text>
      )}
      {vm.scanError && <Text className={classes.error}>{vm.scanError}</Text>}
    </section>
  );
};

interface CategoryRowProps {
  category: StorageCategory;
  color: string | undefined;
  vm: Vm;
  t: T;
  onDelete: (c: StorageCategory) => void;
  onBackup: () => void;
}

const CategoryRow = ({ category: c, color, vm, t, onDelete, onBackup }: CategoryRowProps) => {
  const label = t(c.labelKey);
  const avail = vm.deleteAvailability(c);
  const deleting = vm.feedback.kind === 'deleting' && vm.feedback.category.id === c.id;
  const paths = c.paths.join(', ') + (c.morePaths > 0 ? ` ${t('storage.list.morePaths', { count: c.morePaths })}` : '');

  const deleteButton = c.deletable ? (
    <Button
      size="compact-sm"
      variant={c.risk === 'danger' ? 'outline' : 'light'}
      className={classes.deleteButton}
      data-risk={c.risk}
      leftSection={<IconTrash size={16} stroke={1.75} />}
      disabled={avail.disabled}
      loading={deleting}
      onClick={() => void onDelete(c)}
      aria-label={t('storage.list.deleteAria', { label })}
    >
      {t('storage.list.delete')}
    </Button>
  ) : (
    <span className={classes.locked}>
      <IconLock size={16} stroke={1.75} />
    </span>
  );

  return (
    <div className={classes.row} data-risk={c.risk}>
      <span className={classes.rowSwatch} style={color ? { backgroundColor: color } : undefined} />
      <div className={classes.rowMain}>
        <div className={classes.rowTitle}>
          <Text className={classes.rowLabel}>{label}</Text>
          <span className={classes.risk} data-risk={c.risk}>
            {c.risk === 'danger' && <IconAlertTriangle size={12} stroke={2} />}
            {t(`storage.risk.${c.risk}`)}
          </span>
          {c.deletable && c.requiresStopped && <span className={classes.tag}>{t('storage.stopped')}</span>}
        </div>
        <Text className={classes.rowDesc}>{t(c.descriptionKey)}</Text>
        <Text className={classes.rowMeta}>
          {t('storage.list.meta', { size: formatBytes(c.bytes), files: c.files.toLocaleString() })}
        </Text>
        {paths && <Text className={classes.rowPaths}>{paths}</Text>}
      </div>
      <div className={classes.rowActions}>
        {c.id === 'database' && vm.hasDatabase && (
          <Tooltip label={t('storage.backup.runningHint')} disabled={!vm.running} withArrow>
            <span>
              <Button
                size="compact-sm"
                variant="subtle"
                leftSection={<IconDatabaseExport size={16} stroke={1.75} />}
                disabled={vm.running || vm.busy}
                onClick={() => void onBackup()}
              >
                {t('storage.list.backup')}
              </Button>
            </span>
          </Tooltip>
        )}
        {avail.reasonKey ? (
          <Tooltip label={t(avail.reasonKey)} withArrow multiline maw={260}>
            <span>{deleteButton}</span>
          </Tooltip>
        ) : (
          deleteButton
        )}
      </div>
    </div>
  );
};

interface FeedbackLineProps {
  feedback: StorageFeedback;
  t: T;
}

const FeedbackLine = ({ feedback, t }: FeedbackLineProps) => {
  switch (feedback.kind) {
    case 'idle':
      return null;
    case 'deleting':
      return <Text className={classes.status}>{t('storage.feedback.deleting', { label: t(feedback.category.labelKey) })}</Text>;
    case 'deleted':
      return (
        <Text className={classes.ok}>
          {t('storage.feedback.deleted', {
            label: t(feedback.category.labelKey),
            size: formatBytes(feedback.freedBytes),
            files: feedback.removedFiles,
          })}
        </Text>
      );
    case 'deleteFailed':
      return (
        <Text className={classes.error}>
          {t(`storage.deleteError.${feedback.code || 'failed'}`, {
            label: t(feedback.category.labelKey),
            message: feedback.message,
          })}
        </Text>
      );
    case 'changingHome':
      return <Text className={classes.status}>{t('storage.feedback.changingHome')}</Text>;
    case 'homeChanged':
      return <Text className={classes.ok}>{t('storage.feedback.homeChanged')}</Text>;
    case 'homeReset':
      return <Text className={classes.ok}>{t('storage.feedback.homeReset')}</Text>;
    case 'backingUp':
      return <Text className={classes.status}>{t('storage.feedback.backingUp')}</Text>;
    case 'failed':
      return <Text className={classes.error}>{t('storage.feedback.failed', { message: feedback.message })}</Text>;
  }
};

import { Button, Text } from '@mantine/core';
import { IconBrowser, IconPlayerPlay, IconPlayerStop } from '@tabler/icons-react';
import { useT } from '@/i18n';
import { LauncherService } from '@/services/launcher';
import { useLauncherStatus } from '@/shared/hooks/useLauncherStatus';
import classes from './AppStatus.module.css';

const STATE_KEY: Record<string, string> = {
  stopped: 'systray.appStatus.stopped',
  starting: 'systray.appStatus.starting',
  running: 'systray.appStatus.running',
  stopping: 'systray.appStatus.stopping',
  error: 'systray.appStatus.error',
};

// 托盘面板：NarraFork 服务状态 + 启动 / 停止 / 打开。
export const AppStatus = () => {
  const t = useT();
  const status = useLauncherStatus();
  const running = status.state === 'running';
  const busy = status.state === 'starting' || status.state === 'stopping' || status.preparing;

  return (
    <div className={classes.root}>
      <Text className={classes.sectionTitle}>{t('systray.appStatus.title')}</Text>
      <div className={classes.statusRow}>
        <div className={classes.dot} data-state={status.state} />
        <Text className={classes.statusText}>{t(STATE_KEY[status.state] ?? STATE_KEY.stopped!)}</Text>
      </div>
      {(status.version || status.installed) && (
        <Text className={classes.version}>
          {t('systray.appStatus.version')} {status.version || status.installed}
        </Text>
      )}
      <div className={classes.actions}>
        {running ? (
          <>
            <Button size="xs" variant="light" leftSection={<IconBrowser size={16} stroke={1.75} />} onClick={() => void LauncherService.open()}>
              {t('systray.appStatus.open')}
            </Button>
            <Button size="xs" variant="subtle" leftSection={<IconPlayerStop size={16} stroke={1.75} />} onClick={() => void LauncherService.stop()}>
              {t('systray.appStatus.stop')}
            </Button>
          </>
        ) : (
          <Button
            size="xs"
            variant="light"
            loading={busy}
            leftSection={<IconPlayerPlay size={16} stroke={1.75} />}
            onClick={() => void LauncherService.start()}
          >
            {t('systray.appStatus.start')}
          </Button>
        )}
      </div>
    </div>
  );
};

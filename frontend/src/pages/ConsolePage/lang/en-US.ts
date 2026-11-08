import type { Messages } from '@/i18n';

export const consolePageEnUS: Messages = {
  console: {
    title: 'NarraFork',
    state: {
      stopped: 'Stopped',
      starting: 'Starting',
      running: 'Running',
      stopping: 'Stopping',
      error: 'Error',
    },
    preparing: 'Checking for updates…',
    uptime: 'Up {{time}}',
    actions: {
      start: 'Start NarraFork',
      downloadAndStart: 'Download and start',
      stop: 'Stop',
      cancel: 'Cancel start',
      restart: 'Restart',
      open: 'Open NarraFork',
      openExternal: 'Open in browser',
    },
    logs: 'Server output',
    clear: 'Clear output',
    jumpToEnd: 'Jump to latest',
    empty: 'Output appears here once NarraFork is running…',
  },
};

import type { Messages } from '@/i18n';

export const consolePageZhCN: Messages = {
  console: {
    title: 'NarraFork',
    state: {
      stopped: '已停止',
      starting: '启动中',
      running: '运行中',
      stopping: '正在停止',
      error: '出错',
    },
    preparing: '正在检查更新…',
    uptime: '已运行 {{time}}',
    actions: {
      start: '启动 NarraFork',
      downloadAndStart: '下载并启动',
      stop: '停止',
      cancel: '取消启动',
      restart: '重启',
      open: '打开 NarraFork',
      openExternal: '在浏览器中打开',
    },
    logs: '服务输出',
    clear: '清空输出',
    jumpToEnd: '跳到最新',
    empty: 'NarraFork 运行后输出会显示在这里…',
  },
};

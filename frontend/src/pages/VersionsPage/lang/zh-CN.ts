import type { Messages } from '@/i18n';

export const versionsPageZhCN: Messages = {
  versions: {
    summary: {
      title: '版本信息',
      check: '检查更新',
      installed: '已下载版本',
      latest: '当前通道最新',
      platform: '平台',
      none: '未下载',
    },
    prerelease: '预发布',
    stable: '稳定版',
    current: '当前',
    downloading: '正在下载 v{{version}}：{{received}} / {{total}}（{{speed}}/s）',
    verifying: '正在校验 SHA-256…',
    cancel: '取消',
    updateTo: '更新到 v{{version}}',
    downloadLatest: '下载 v{{version}}',
    upToDate: '已是当前通道的最新版本',
    restartToApply: '重启以使用 v{{version}}',
    dataDir: '下载目录',
    reveal: '打开目录',
    list: {
      title: '可下载版本',
      hint: '可以安装任意版本（包括回退到旧版）；安装后在下次启动或重启时生效。',
    },
    notes: '更新日志',
    install: '安装',
    installed: '已安装',
  },
};

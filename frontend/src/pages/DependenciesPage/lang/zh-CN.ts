import type { Messages } from '@/i18n';

export const dependenciesPageZhCN: Messages = {
  deps: {
    tools: '必需与推荐工具',
    refresh: '重新检测',
    required: '必需',
    recommended: '推荐',
    install: '安装',
    items: {
      git: { label: 'Git', description: '版本控制工具，NarraFork 运行必需' },
      rg: { label: 'ripgrep', description: '快速搜索工具，提升代码搜索性能' },
      podman: { label: 'Podman', description: '容器运行时，用于插件与基准测试（可选）' },
    },
    git: {
      title: 'Git 身份',
      description: '设置全局 git user.name 与 user.email，用于提交。',
      name: '用户名',
      namePlaceholder: '你的名字',
      email: '邮箱',
      emailPlaceholder: 'your@email.com',
      save: '保存 Git 配置',
      saved: '已保存',
    },
  },
};

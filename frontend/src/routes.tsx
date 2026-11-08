import { IconDatabase, IconPackage, IconRefresh, IconSettings, IconTerminal2 } from '@tabler/icons-react';
import { lazy } from 'react';
import { ConsolePage } from '@/pages/ConsolePage/ConsolePage';
import { SettingsPageSkeleton } from '@/pages/SettingsPage/SettingsPage.skeleton';
import type { RouteDefinition } from '@/router';

// SettingsPage 走 React.lazy：调色板编辑器较重，首屏不进入 main bundle。
// 注意：lazy import 必须从具体子文件导入，不要从 index.ts 走。
const SettingsPage = lazy(async () => {
  const mod = await import('@/pages/SettingsPage/SettingsPage');
  return { default: mod.SettingsPage };
});

const VersionsPage = lazy(async () => {
  const mod = await import('@/pages/VersionsPage/VersionsPage');
  return { default: mod.VersionsPage };
});

const DependenciesPage = lazy(async () => {
  const mod = await import('@/pages/DependenciesPage/DependenciesPage');
  return { default: mod.DependenciesPage };
});

const StoragePage = lazy(async () => {
  const mod = await import('@/pages/StoragePage/StoragePage');
  return { default: mod.StoragePage };
});

// 路由表是唯一事实源（导航条目自动从这里渲染）。
// 控制台 keepAlive：保留日志滚动位置与订阅。
export const routes: RouteDefinition[] = [
  {
    id: 'console',
    labelKey: 'route.console',
    label: 'Console',
    icon: IconTerminal2,
    element: <ConsolePage />,
    slot: 'primary',
    keepAlive: true,
  },
  {
    id: 'versions',
    labelKey: 'route.versions',
    label: 'Versions',
    icon: IconRefresh,
    element: <VersionsPage />,
    slot: 'primary',
  },
  {
    id: 'dependencies',
    labelKey: 'route.dependencies',
    label: 'Dependencies',
    icon: IconPackage,
    element: <DependenciesPage />,
    slot: 'primary',
  },
  {
    id: 'storage',
    labelKey: 'route.storage',
    label: 'Storage',
    icon: IconDatabase,
    element: <StoragePage />,
    slot: 'primary',
  },
  {
    id: 'settings',
    labelKey: 'route.settings',
    label: 'Settings',
    icon: IconSettings,
    element: <SettingsPage />,
    fallback: <SettingsPageSkeleton />,
    slot: 'footer',
  },
];

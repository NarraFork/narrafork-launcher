import { AppLayout } from '@/components/AppLayout';
import { I18nProvider, registerFoundationLocales } from '@/i18n';
import { registerConsolePageLocales } from '@/pages/ConsolePage/lang';
import { registerDependenciesPageLocales } from '@/pages/DependenciesPage/lang';
import { registerSettingsPageLocales } from '@/pages/SettingsPage/lang';
import { registerStoragePageLocales } from '@/pages/StoragePage/lang';
import { registerVersionsPageLocales } from '@/pages/VersionsPage/lang';
import { PreferencesProvider } from '@/preferences';
import { RouterProvider } from '@/router';
import { routes } from '@/routes';
import { FoundationThemeProvider, registerFoundationThemes } from '@/styles/themes';

// 启动时注册：
//  1. 内置主题预设（light / dark / obsidian + 已保存的 custom）
//  2. 内置 locale 公用文案（zh-CN / en-US）
//  3. 各页面的语言包（深合并到对应 locale 上）
// 所有 register* 都是幂等；页面 locale 必须在 registerFoundationLocales() 之后注册。
registerFoundationThemes();
registerFoundationLocales();
registerConsolePageLocales();
registerVersionsPageLocales();
registerDependenciesPageLocales();
registerStoragePageLocales();
registerSettingsPageLocales();

// Provider 嵌套顺序（自外向内）：I18n → Theme → Preferences → Router
export const App = () => {
  return (
    <I18nProvider>
      <FoundationThemeProvider>
        <PreferencesProvider>
          <RouterProvider routes={routes}>
            <AppLayout />
          </RouterProvider>
        </PreferencesProvider>
      </FoundationThemeProvider>
    </I18nProvider>
  );
};

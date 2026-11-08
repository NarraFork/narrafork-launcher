import { localeRegistry } from '@/i18n';
import { launcherSettingsZhCN } from './zh-CN';
import { launcherSettingsEnUS } from './en-US';

let registered = false;

export const registerLauncherSettingsLocales = (): void => {
  if (registered) return;
  localeRegistry.extend('zh-CN', launcherSettingsZhCN);
  localeRegistry.extend('en-US', launcherSettingsEnUS);
  registered = true;
};

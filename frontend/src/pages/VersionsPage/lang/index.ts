import { localeRegistry } from '@/i18n';
import { versionsPageZhCN } from './zh-CN';
import { versionsPageEnUS } from './en-US';

let registered = false;

export const registerVersionsPageLocales = (): void => {
  if (registered) return;
  localeRegistry.extend('zh-CN', versionsPageZhCN);
  localeRegistry.extend('en-US', versionsPageEnUS);
  registered = true;
};

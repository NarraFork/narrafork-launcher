import { localeRegistry } from '@/i18n';
import { consolePageZhCN } from './zh-CN';
import { consolePageEnUS } from './en-US';

let registered = false;

export const registerConsolePageLocales = (): void => {
  if (registered) return;
  localeRegistry.extend('zh-CN', consolePageZhCN);
  localeRegistry.extend('en-US', consolePageEnUS);
  registered = true;
};

import { localeRegistry } from '@/i18n';
import { storagePageZhCN } from './zh-CN';
import { storagePageEnUS } from './en-US';

let registered = false;

export const registerStoragePageLocales = (): void => {
  if (registered) return;
  localeRegistry.extend('zh-CN', storagePageZhCN);
  localeRegistry.extend('en-US', storagePageEnUS);
  registered = true;
};

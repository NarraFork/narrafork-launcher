import { localeRegistry } from '@/i18n';
import { dependenciesPageZhCN } from './zh-CN';
import { dependenciesPageEnUS } from './en-US';

let registered = false;

export const registerDependenciesPageLocales = (): void => {
  if (registered) return;
  localeRegistry.extend('zh-CN', dependenciesPageZhCN);
  localeRegistry.extend('en-US', dependenciesPageEnUS);
  registered = true;
};

import { Badge, Button, Text, TextInput } from '@mantine/core';
import { IconCheck, IconDownload, IconRefresh, IconX } from '@tabler/icons-react';
import { useT } from '@/i18n';
import { useDependenciesPage } from './useDependenciesPage';
import classes from './DependenciesPage.module.css';

// 依赖：Git（必需）/ ripgrep（推荐）/ Podman（Linux 可选）检测 + Git 身份配置。
export const DependenciesPage = () => {
  const t = useT();
  const vm = useDependenciesPage();

  return (
    <div className={classes.root}>
      <div className={classes.body}>
        <section className={classes.card}>
          <div className={classes.cardHeader}>
            <Text className={classes.cardTitle}>{t('deps.tools')}</Text>
            <Button
              size="compact-sm"
              variant="subtle"
              leftSection={<IconRefresh size={16} stroke={1.75} />}
              loading={vm.loading}
              onClick={() => void vm.refresh()}
            >
              {t('deps.refresh')}
            </Button>
          </div>
          <div className={classes.list}>
            {vm.deps.map((d) => (
              <div key={d.name} className={classes.row}>
                <span className={classes.icon} data-ok={d.installed || undefined} aria-hidden>
                  {d.installed ? <IconCheck size={18} stroke={1.75} /> : <IconX size={18} stroke={1.75} />}
                </span>
                <div className={classes.main}>
                  <div className={classes.titleRow}>
                    <Text className={classes.name}>{t(`deps.items.${d.name}.label`)}</Text>
                    <Badge size="xs" variant={d.required ? 'filled' : 'light'}>
                      {d.required ? t('deps.required') : t('deps.recommended')}
                    </Badge>
                  </div>
                  <Text className={classes.desc}>
                    {d.installed ? d.version || d.path : t(`deps.items.${d.name}.description`)}
                  </Text>
                </div>
                {!d.installed && (
                  <Button
                    size="compact-sm"
                    variant="light"
                    leftSection={<IconDownload size={16} stroke={1.75} />}
                    onClick={() => vm.openInstall(d.installUrl)}
                  >
                    {t('deps.install')}
                  </Button>
                )}
              </div>
            ))}
          </div>
        </section>

        <section className={classes.card}>
          <Text className={classes.cardTitle}>{t('deps.git.title')}</Text>
          <Text className={classes.hint}>{t('deps.git.description')}</Text>
          <div className={classes.form}>
            <TextInput
              label={t('deps.git.name')}
              placeholder={t('deps.git.namePlaceholder')}
              value={vm.identity.name}
              onChange={(e) => vm.setIdentity({ ...vm.identity, name: e.currentTarget.value })}
              disabled={!vm.gitInstalled}
            />
            <TextInput
              label={t('deps.git.email')}
              placeholder={t('deps.git.emailPlaceholder')}
              value={vm.identity.email}
              onChange={(e) => vm.setIdentity({ ...vm.identity, email: e.currentTarget.value })}
              disabled={!vm.gitInstalled}
            />
          </div>
          <div className={classes.actions}>
            <Button onClick={() => void vm.saveIdentity()} loading={vm.saving} disabled={!vm.gitInstalled}>
              {t('deps.git.save')}
            </Button>
            {vm.saved && <Text className={classes.ok}>{t('deps.git.saved')}</Text>}
            {vm.error && <Text className={classes.error}>{vm.error}</Text>}
          </div>
        </section>
      </div>
    </div>
  );
};

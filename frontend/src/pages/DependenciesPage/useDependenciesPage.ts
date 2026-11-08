import { useCallback, useEffect, useState } from 'react';
import { LauncherService, type Dependency, type GitIdentity } from '@/services/launcher';

export const useDependenciesPage = () => {
  const [deps, setDeps] = useState<Dependency[]>([]);
  const [loading, setLoading] = useState(false);
  const [identity, setIdentity] = useState<GitIdentity>({ name: '', email: '' });
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const [d, id] = await Promise.all([LauncherService.checkDependencies(), LauncherService.getGitIdentity()]);
      setDeps(d);
      setIdentity(id);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const saveIdentity = useCallback(async () => {
    setSaving(true);
    setSaved(false);
    setError(null);
    try {
      await LauncherService.setGitIdentity(identity);
      setSaved(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }, [identity]);

  return {
    deps,
    loading,
    refresh,
    identity,
    setIdentity: (id: GitIdentity) => {
      setSaved(false);
      setIdentity(id);
    },
    saveIdentity,
    saving,
    saved,
    error,
    gitInstalled: deps.some((d) => d.name === 'git' && d.installed),
    openInstall: (url: string) => void LauncherService.openURL(url),
  };
};

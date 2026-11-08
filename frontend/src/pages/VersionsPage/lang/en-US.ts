import type { Messages } from '@/i18n';

export const versionsPageEnUS: Messages = {
  versions: {
    summary: {
      title: 'Version',
      check: 'Check for updates',
      installed: 'Downloaded version',
      latest: 'Latest on channel',
      platform: 'Platform',
      none: 'Not downloaded',
    },
    prerelease: 'Pre-release',
    stable: 'Stable',
    current: 'Current',
    downloading: 'Downloading v{{version}}: {{received}} / {{total}} ({{speed}}/s)',
    verifying: 'Verifying SHA-256…',
    cancel: 'Cancel',
    updateTo: 'Update to v{{version}}',
    downloadLatest: 'Download v{{version}}',
    upToDate: 'You have the latest version on this channel',
    restartToApply: 'Restart to use v{{version}}',
    dataDir: 'Download folder',
    reveal: 'Open folder',
    list: {
      title: 'Available versions',
      hint: 'Install any version, including an older one; it takes effect on the next start or restart.',
    },
    notes: 'Release notes',
    install: 'Install',
    installed: 'Installed',
  },
};

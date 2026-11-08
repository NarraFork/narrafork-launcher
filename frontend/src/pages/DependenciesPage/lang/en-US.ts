import type { Messages } from '@/i18n';

export const dependenciesPageEnUS: Messages = {
  deps: {
    tools: 'Required and recommended tools',
    refresh: 'Check again',
    required: 'Required',
    recommended: 'Recommended',
    install: 'Install',
    items: {
      git: { label: 'Git', description: 'Version control; NarraFork needs it to run' },
      rg: { label: 'ripgrep', description: 'Fast search tool for quicker code search' },
      podman: { label: 'Podman', description: 'Container runtime for plugins and benchmarks (optional)' },
    },
    git: {
      title: 'Git identity',
      description: 'Sets the global git user.name and user.email used for commits.',
      name: 'Name',
      namePlaceholder: 'Your name',
      email: 'Email',
      emailPlaceholder: 'your@email.com',
      save: 'Save Git config',
      saved: 'Saved',
    },
  },
};

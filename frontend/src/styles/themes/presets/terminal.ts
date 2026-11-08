import type { TerminalPalette } from '../types';

// 控制台终端调色板（两套）：
//  - 深色：沿用旧启动器的 #1e1e1e / #d4d4d4，ANSI 色取 One Dark 系并整体提亮，
//    black 用可读的灰（真黑在深底上看不见）。
//  - 浅色：GitHub Light 系，浅底深字；white / brightWhite 映射为可读的灰而不是纯白，
//    yellow 用琥珀色、cyan 用深青，保证浅底上的对比度。
// 对比度目标（相对 background）：foreground ≥ 7:1，muted 与 16 色 ≥ 3:1。
// 预设通过 palette.terminal 选用其中一套；没有 terminal 的主题（用户自定义）按 mode 回落。

export const TERMINAL_DARK: TerminalPalette = {
  background: '#1e1e1e',
  foreground: '#d4d4d4',
  muted: '#8c8c8c',
  selection: '#264f78',
  black: '#6e7681',
  red: '#e06c75',
  green: '#6bc96b',
  yellow: '#e5c07b',
  blue: '#61afef',
  magenta: '#c678dd',
  cyan: '#56b6c2',
  white: '#d4d4d4',
  brightBlack: '#8b949e',
  brightRed: '#ff8a93',
  brightGreen: '#8fe08f',
  brightYellow: '#f2d49b',
  brightBlue: '#8cc8ff',
  brightMagenta: '#dc9ef0',
  brightCyan: '#7fd4de',
  brightWhite: '#ffffff',
};

export const TERMINAL_LIGHT: TerminalPalette = {
  background: '#f6f8fa',
  foreground: '#1f2328',
  muted: '#59636e',
  selection: '#b6d7ff',
  black: '#24292f',
  red: '#cf222e',
  green: '#1a7f37',
  yellow: '#9a6700',
  blue: '#0969da',
  magenta: '#8250df',
  cyan: '#1b7c83',
  white: '#6e7781',
  brightBlack: '#57606a',
  brightRed: '#d73a49',
  brightGreen: '#22863a',
  brightYellow: '#a06f00',
  brightBlue: '#1f7ae0',
  brightMagenta: '#8a63d2',
  brightCyan: '#1f8a96',
  brightWhite: '#7d8590',
};

// 主题没有声明 terminal 时的默认终端：跟随主题明暗。
export const terminalPaletteFor = (mode: 'light' | 'dark'): TerminalPalette =>
  mode === 'dark' ? TERMINAL_DARK : TERMINAL_LIGHT;

import type { MantineThemeOverride } from '@mantine/core';

// FoundationPalette：脚手架专属调色板，编译为 CSS 变量 --fd-*（见 buildMantineTheme.ts），
// JS 侧通过 useFoundationPalette() 读取。
// 所有组件样式只能消费这里定义的语义颜色，不允许硬编码十六进制。
export interface FoundationPalette {
  bg: {
    base: string;       // 标题栏 / 最外层背景
    sidebar: string;    // 侧边栏
    content: string;    // 主内容区（页面默认背景）
    surface: string;    // 卡片 / Paper 表面
    elevated: string;   // 输入框 / 提升层（Tooltip 内层等）
    hover: string;      // hover 半透明覆盖
    active: string;     // active / selected 半透明覆盖
  };
  text: {
    primary: string;
    secondary: string;
    muted: string;
  };
  divider: string;
  accent: string;
  accentHover: string;
  status: {
    danger: string;
    success: string;
    warning: string;
  };
  // 终端（控制台）配色，编译为 --fd-term-*。可选：缺省时按 mode 回落到 presets/terminal.ts
  // 的浅色 / 深色默认终端（用户自定义主题存的是 JSON，不带这一组）。
  terminal?: TerminalPalette;
}

// 终端调色板：背景 / 前景 / 次要文字 / 选区 + ANSI 16 色（SGR 30–37 / 90–97 / 40–47 / 100–107）。
// 每种颜色都必须在 background 上清晰可读（浅色方案里 white / brightWhite 也要是可读的灰）。
export interface TerminalPalette {
  background: string;
  foreground: string;
  muted: string;
  selection: string;
  black: string;
  red: string;
  green: string;
  yellow: string;
  blue: string;
  magenta: string;
  cyan: string;
  white: string;
  brightBlack: string;
  brightRed: string;
  brightGreen: string;
  brightYellow: string;
  brightBlue: string;
  brightMagenta: string;
  brightCyan: string;
  brightWhite: string;
}

// 主题预设：注册到 themeRegistry，可在运行时切换。
//  - name：唯一标识（必填）
//  - label：UI 显示名（如设置页里的下拉项）
//  - mode：决定 Mantine color scheme（forceColorScheme）
//  - palette：FoundationPalette，会编译为 --fd-* CSS 变量 + Mantine primary 色阶
//  - mantineOverrides：进一步覆盖 Mantine theme（可选，深合并）
export interface FoundationThemePreset {
  name: string;
  label: string;
  mode: 'light' | 'dark';
  palette: FoundationPalette;
  mantineOverrides?: MantineThemeOverride;
}

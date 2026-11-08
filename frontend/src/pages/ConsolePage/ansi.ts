// 把一行服务输出解析为带样式的片段：支持 SGR 颜色码与 NarraFork 的 JSON 日志行。
// 只返回数据（class 名 / rgb 字符串），渲染交给 View。

export type Tone = 'default' | 'muted' | 'danger' | 'success' | 'warning' | 'accent' | 'info';

export interface Span {
  text: string;
  tone?: Tone;
  rgb?: string; // 256 色 / 真彩色时的具体颜色
  bold?: boolean;
  dim?: boolean;
}

export interface FormattedLine {
  spans: Span[];
  tone: Tone;
}

// SGR 30–37 / 90–97 → 语义色调（跟随主题，不写死色值）
const BASIC: Tone[] = ['muted', 'danger', 'success', 'warning', 'info', 'accent', 'info', 'default'];

const xterm256 = (n: number): string | undefined => {
  if (n < 16) return undefined;
  if (n < 232) {
    const i = n - 16;
    return `rgb(${Math.floor(i / 36) * 51},${Math.floor((i % 36) / 6) * 51},${(i % 6) * 51})`;
  }
  const v = (n - 232) * 10 + 8;
  return `rgb(${v},${v},${v})`;
};

// eslint-disable-next-line no-control-regex
const SGR = /\x1b\[([0-9;]*)m/g;
// eslint-disable-next-line no-control-regex
const ANY_ESC = /\x1b\[[0-9;?]*[A-Za-z]/g;

const parseAnsi = (raw: string, base: Tone): Span[] => {
  const spans: Span[] = [];
  let last = 0;
  let tone: Tone | undefined;
  let rgb: string | undefined;
  let bold = false;
  let dim = false;
  SGR.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = SGR.exec(raw)) !== null) {
    if (m.index > last) spans.push({ text: raw.slice(last, m.index).replace(ANY_ESC, ''), tone: tone ?? base, rgb, bold, dim });
    last = m.index + m[0].length;
    const codes = m[1] ? m[1].split(';').map(Number) : [0];
    for (let i = 0; i < codes.length; i++) {
      const c = codes[i] ?? 0;
      if (c === 0) { tone = undefined; rgb = undefined; bold = false; dim = false; }
      else if (c === 1) bold = true;
      else if (c === 2) dim = true;
      else if (c === 22) { bold = false; dim = false; }
      else if (c >= 30 && c <= 37) { tone = BASIC[c - 30]; rgb = undefined; }
      else if (c >= 90 && c <= 97) { tone = BASIC[c - 90]; rgb = undefined; }
      else if (c === 39) { tone = undefined; rgb = undefined; }
      else if (c === 38 && codes[i + 1] === 5) {
        const n = codes[i + 2] ?? 0;
        if (n < 16) { tone = BASIC[n % 8]; rgb = undefined; } else rgb = xterm256(n);
        i += 2;
      } else if (c === 38 && codes[i + 1] === 2) {
        rgb = `rgb(${codes[i + 2] ?? 0},${codes[i + 3] ?? 0},${codes[i + 4] ?? 0})`;
        i += 4;
      }
    }
  }
  if (last < raw.length) spans.push({ text: raw.slice(last).replace(ANY_ESC, ''), tone: tone ?? base, rgb, bold, dim });
  return spans.filter((s) => s.text !== '');
};

const LEVEL_TONE: Record<string, Tone> = {
  trace: 'muted',
  debug: 'muted',
  info: 'success',
  warn: 'warning',
  warning: 'warning',
  error: 'danger',
  fatal: 'danger',
};

const fmtValue = (v: unknown): string => (typeof v === 'string' ? v : JSON.stringify(v));

export const formatLine = (raw: string, stream: 'stdout' | 'stderr' | 'system'): FormattedLine => {
  const base: Tone = stream === 'stderr' ? 'danger' : stream === 'system' ? 'accent' : 'default';
  const clean = raw.replace(ANY_ESC, '').trim();
  if (clean.startsWith('{') && clean.endsWith('}')) {
    try {
      const obj = JSON.parse(clean) as Record<string, unknown>;
      if (obj && typeof obj.msg === 'string') {
        const level = String(obj.level ?? 'info').toLowerCase();
        const tone = LEVEL_TONE[level] ?? 'default';
        const ts = obj.ts ?? obj.time;
        const time = typeof ts === 'string' || typeof ts === 'number' ? new Date(ts).toLocaleTimeString() : '';
        const extra = Object.entries(obj)
          .filter(([k]) => !['ts', 'time', 'level', 'msg'].includes(k))
          .map(([k, v]) => `${k}=${fmtValue(v)}`)
          .join(' ');
        const spans: Span[] = [];
        if (time) spans.push({ text: `${time} `, tone: 'muted' });
        spans.push({ text: `${level.toUpperCase().padEnd(5)} `, tone, bold: true });
        spans.push({ text: obj.msg, tone: tone === 'success' ? 'default' : tone });
        if (extra) spans.push({ text: ` ${extra}`, tone: 'muted' });
        return { spans, tone };
      }
    } catch {
      // 不是 JSON，按普通文本处理
    }
  }
  return { spans: parseAnsi(raw, base), tone: base };
};

// 控制台行解析（纯函数、零依赖，可直接用 node 跑单测）：
//  1. parseAnsi：ANSI / VT 转义 → 带样式片段。
//     - SGR：0 1 2 3 4 7 9 21 22 23 24 27 29、30–37 / 90–97、38;5;n、38;2;r;g;b、39、
//       40–47 / 100–107、48;5;n、48;2;r;g;b、49，以及冒号子参数形式（38:5:n、38:2::r:g:b、4:3 …）
//     - 其它 CSI（光标移动 / 擦除 / 私有模式）、OSC（ESC]…BEL / ESC\）、DCS / SOS / PM / APC、
//       单字符转义与 C0 控制符一律剥离
//     - \r 回车覆盖：回车之后若还有文字，只保留最后一个回车之后的内容（样式状态延续）
//  2. formatLine：按流类型格式化一行；NarraFork 的 JSON 日志（stdout / stderr 皆可）按级别着色。
// 只输出数据（ANSI 色号 / rgb() / 语义角色），颜色由 View 映射为 --fd-term-* 变量。

export type LogStream = 'stdout' | 'stderr' | 'system';

// 0–15：ANSI 16 色色号；'fg' / 'bg'：终端默认前景 / 背景（反显时出现）；rgb()：256 色 / 真彩色
export type TermColor = number | 'fg' | 'bg' | `rgb(${number},${number},${number})`;

export type UnderlineStyle = 'single' | 'double' | 'curly';

// JSON 日志片段的语义角色（View 按角色 + 行级别着色）
export type SpanRole = 'time' | 'level' | 'msg' | 'key' | 'value';

export interface Span {
  text: string;
  role?: SpanRole;
  fg?: TermColor;
  bg?: TermColor;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: UnderlineStyle;
  strike?: boolean;
}

export type LogLevel = 'debug' | 'info' | 'warn' | 'error';

// stdout / stderr：普通文本（stderr 整行偏红）；system：启动器自己的提示；log：JSON 日志
export type LineKind = 'stdout' | 'stderr' | 'system' | 'log';

export interface FormattedLine {
  kind: LineKind;
  level?: LogLevel;
  spans: Span[];
}

// —— SGR 状态 ——

interface SgrState {
  fg?: TermColor;
  bg?: TermColor;
  bold: boolean;
  dim: boolean;
  italic: boolean;
  underline?: UnderlineStyle;
  inverse: boolean;
  strike: boolean;
}

const freshState = (): SgrState => ({ bold: false, dim: false, italic: false, inverse: false, strike: false });

const toInt = (s: string | undefined): number => {
  const n = s ? parseInt(s, 10) : 0;
  return Number.isNaN(n) ? 0 : n;
};

const clampByte = (n: number): number => Math.max(0, Math.min(255, n));

const rgb = (r: number, g: number, b: number): TermColor =>
  `rgb(${clampByte(r)},${clampByte(g)},${clampByte(b)})`;

// xterm 256 色：0–15 走主题 16 色，16–231 为 6×6×6 色立方，232–255 为灰阶
const CUBE = [0, 95, 135, 175, 215, 255];
export const color256 = (n: number): TermColor => {
  const c = clampByte(n);
  if (c < 16) return c;
  if (c < 232) {
    const i = c - 16;
    return rgb(CUBE[Math.floor(i / 36)] ?? 0, CUBE[Math.floor(i / 6) % 6] ?? 0, CUBE[i % 6] ?? 0);
  }
  const v = (c - 232) * 10 + 8;
  return rgb(v, v, v);
};

const setColor = (st: SgrState, target: number, color: TermColor): void => {
  if (target === 38) st.fg = color;
  else if (target === 48) st.bg = color;
  // 58（下划线颜色）只消费参数，不渲染
};

// 冒号子参数：38:5:n / 38:2:r:g:b / 38:2:<色彩空间>:r:g:b / 4:0–3
const applyColonParam = (st: SgrState, sub: string[]): void => {
  const head = toInt(sub[0]);
  if (head === 4) {
    const style = toInt(sub[1]);
    st.underline = style === 0 ? undefined : style === 2 ? 'double' : style === 3 ? 'curly' : 'single';
    return;
  }
  if (head !== 38 && head !== 48 && head !== 58) return;
  const mode = toInt(sub[1]);
  if (mode === 5) setColor(st, head, color256(toInt(sub[2])));
  else if (mode === 2) {
    const o = sub.length >= 6 ? 3 : 2;
    setColor(st, head, rgb(toInt(sub[o]), toInt(sub[o + 1]), toInt(sub[o + 2])));
  }
};

const applySgr = (st: SgrState, raw: string): void => {
  const params = raw.split(';');
  for (let i = 0; i < params.length; i++) {
    const p = params[i] ?? '';
    if (p.includes(':')) {
      applyColonParam(st, p.split(':'));
      continue;
    }
    const c = toInt(p);
    if (c === 0) Object.assign(st, freshState(), { fg: undefined, bg: undefined, underline: undefined });
    else if (c === 1) st.bold = true;
    else if (c === 2) st.dim = true;
    else if (c === 3) st.italic = true;
    else if (c === 4) st.underline = 'single';
    else if (c === 7) st.inverse = true;
    else if (c === 9) st.strike = true;
    else if (c === 21) st.underline = 'double';
    else if (c === 22) { st.bold = false; st.dim = false; }
    else if (c === 23) st.italic = false;
    else if (c === 24) st.underline = undefined;
    else if (c === 27) st.inverse = false;
    else if (c === 29) st.strike = false;
    else if (c >= 30 && c <= 37) st.fg = c - 30;
    else if (c === 39) st.fg = undefined;
    else if (c >= 40 && c <= 47) st.bg = c - 40;
    else if (c === 49) st.bg = undefined;
    else if (c >= 90 && c <= 97) st.fg = c - 90 + 8;
    else if (c >= 100 && c <= 107) st.bg = c - 100 + 8;
    else if (c === 38 || c === 48 || c === 58) {
      const mode = toInt(params[i + 1]);
      if (mode === 5) {
        setColor(st, c, color256(toInt(params[i + 2])));
        i += 2;
      } else if (mode === 2) {
        setColor(st, c, rgb(toInt(params[i + 2]), toInt(params[i + 3]), toInt(params[i + 4])));
        i += 4;
      } else {
        i += 1;
      }
    }
    // 其它（闪烁 / 隐藏 / 字体 / 上划线…）忽略
  }
};

const spanFrom = (st: SgrState, text: string): Span => {
  let { fg, bg } = st;
  if (st.inverse) {
    [fg, bg] = [bg ?? 'bg', fg ?? 'fg'];
  }
  const span: Span = { text };
  if (fg !== undefined) span.fg = fg;
  if (bg !== undefined) span.bg = bg;
  if (st.bold) span.bold = true;
  if (st.dim) span.dim = true;
  if (st.italic) span.italic = true;
  if (st.underline) span.underline = st.underline;
  if (st.strike) span.strike = true;
  return span;
};

const sameStyle = (a: Span, b: Span): boolean =>
  a.role === b.role &&
  a.fg === b.fg &&
  a.bg === b.bg &&
  a.bold === b.bold &&
  a.dim === b.dim &&
  a.italic === b.italic &&
  a.underline === b.underline &&
  a.strike === b.strike;

// 依次匹配：CSI（含参数 / 中间字节 / 终止字节）、OSC（BEL 或 ST 结束，未结束则吃到行尾）、
// DCS / SOS / PM / APC（ST 结束）、其它 ESC 序列、孤立 ESC、回车（\r\n 中的 \r 直接丢弃）、
// 其余 C0 控制符（保留 \t \n）
const TOKEN =
  // eslint-disable-next-line no-control-regex
  /\x1b\[([\x30-\x3f]*)([\x20-\x2f]*)([\x40-\x7e])|\x1b\][\s\S]*?(?:\x07|\x1b\\|$)|\x1b[PX^_][\s\S]*?(?:\x1b\\|$)|\x1b[\x20-\x2f]*[\x30-\x7e]|\x1b|(\r(?!\n))|[\x00-\x08\x0b-\x1f\x7f]/g;

// 回车覆盖：丢掉当前行（最后一个 \n 之后）已输出的内容
const dropCurrentRow = (spans: Span[]): void => {
  for (let i = spans.length - 1; i >= 0; i--) {
    const s = spans[i]!;
    const nl = s.text.lastIndexOf('\n');
    if (nl >= 0) {
      s.text = s.text.slice(0, nl + 1);
      spans.length = i + 1;
      return;
    }
  }
  spans.length = 0;
};

export const parseAnsi = (raw: string): Span[] => {
  const spans: Span[] = [];
  const st = freshState();
  let pendingCR = false;
  let last = 0;

  const push = (text: string) => {
    if (!text) return;
    if (pendingCR) {
      dropCurrentRow(spans);
      pendingCR = false;
    }
    const span = spanFrom(st, text);
    const prev = spans[spans.length - 1];
    if (prev && sameStyle(prev, span)) prev.text += text;
    else spans.push(span);
  };

  TOKEN.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = TOKEN.exec(raw)) !== null) {
    push(raw.slice(last, m.index));
    last = m.index + m[0].length;
    if (m[4]) {
      pendingCR = true;
    } else if (m[3] === 'm' && !m[2] && !/^[<=>?]/.test(m[1] ?? '')) {
      applySgr(st, m[1] ?? '');
    }
  }
  push(raw.slice(last));
  return spans;
};

export const stripAnsi = (raw: string): string => parseAnsi(raw).map((s) => s.text).join('');

// —— JSON 日志 ——

const LEVEL_ALIASES: Record<string, [LogLevel, string]> = {
  trace: ['debug', 'TRACE'],
  debug: ['debug', 'DEBUG'],
  info: ['info', 'INFO'],
  notice: ['info', 'INFO'],
  warn: ['warn', 'WARN'],
  warning: ['warn', 'WARN'],
  error: ['error', 'ERROR'],
  err: ['error', 'ERROR'],
  critical: ['error', 'CRIT'],
  fatal: ['error', 'FATAL'],
  panic: ['error', 'PANIC'],
  dpanic: ['error', 'PANIC'],
};

const TAG_WIDTH = 5;

// 级别 → [语义级别, 定宽标签]；兼容字符串级别与 pino 风格数字级别
const resolveLevel = (v: unknown): [LogLevel, string] => {
  if (typeof v === 'number') {
    if (v >= 60) return ['error', 'FATAL'];
    if (v >= 50) return ['error', 'ERROR'];
    if (v >= 40) return ['warn', 'WARN'];
    if (v >= 30) return ['info', 'INFO'];
    return v >= 20 ? ['debug', 'DEBUG'] : ['debug', 'TRACE'];
  }
  if (typeof v === 'string' && v) {
    const known = LEVEL_ALIASES[v.toLowerCase()];
    return known ?? ['info', v.toUpperCase().slice(0, TAG_WIDTH)];
  }
  return ['info', 'INFO'];
};

const pad2 = (n: number): string => String(n).padStart(2, '0');

// 本地时间 HH:MM:SS
export const formatClock = (ms: number): string => {
  const d = new Date(ms);
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`;
};

// ISO 字符串 / 毫秒 / 秒（zap 的浮点 epoch）→ 毫秒
const parseTimestamp = (v: unknown): number | undefined => {
  if (typeof v === 'number' && Number.isFinite(v)) return v < 1e12 ? v * 1000 : v;
  if (typeof v === 'string' && v) {
    const ms = Date.parse(v);
    return Number.isNaN(ms) ? undefined : ms;
  }
  return undefined;
};

// logfmt 风格：含空白 / 引号 / 等号的字符串加引号（转义引号与控制符），其余原样；对象 / 数组紧凑 JSON
const QUOTE_ESCAPES: Record<string, string> = { '"': '\\"', '\n': '\\n', '\r': '\\r', '\t': '\\t' };
const formatValue = (v: unknown): string => {
  if (typeof v !== 'string') return JSON.stringify(v) ?? String(v);
  const s = cleanText(v);
  if (s !== '' && !/[\s"=]/.test(s)) return s;
  return `"${s.replace(/["\n\r\t]/g, (c) => QUOTE_ESCAPES[c] ?? c)}"`;
};

// JSON 字符串值里可能带转义后的 ESC / 控制符（JSON.parse 后变成真字符），显示前剥掉
// eslint-disable-next-line no-control-regex
const CONTROL = /[\x00-\x08\x0b-\x1f\x7f]/;
const cleanText = (s: string): string => (CONTROL.test(s) ? stripAnsi(s) : s);

const firstKey = (obj: Record<string, unknown>, keys: string[]): string | undefined =>
  keys.find((k) => Object.prototype.hasOwnProperty.call(obj, k));

const TIME_KEYS = ['ts', 'time', 'timestamp'];
const LEVEL_KEYS = ['level', 'lvl', 'severity'];
const MSG_KEYS = ['msg', 'message'];

const formatJsonLog = (obj: Record<string, unknown>, receivedAt: number | undefined): FormattedLine | null => {
  const msgKey = firstKey(obj, MSG_KEYS);
  const levelKey = firstKey(obj, LEVEL_KEYS);
  const msg = msgKey ? obj[msgKey] : undefined;
  if (typeof msg !== 'string' && levelKey === undefined) return null;

  const timeKey = firstKey(obj, TIME_KEYS);
  const [level, tag] = resolveLevel(levelKey ? obj[levelKey] : undefined);
  const at = (timeKey ? parseTimestamp(obj[timeKey]) : undefined) ?? receivedAt;

  const spans: Span[] = [];
  if (at !== undefined) spans.push({ text: formatClock(at), role: 'time' }, { text: ' ' });
  spans.push({ text: tag.padEnd(TAG_WIDTH), role: 'level', bold: true }, { text: ' ' });
  spans.push({ text: msg === undefined ? '' : typeof msg === 'string' ? cleanText(msg) : formatValue(msg), role: 'msg' });
  for (const [k, v] of Object.entries(obj)) {
    if (k === msgKey || k === levelKey || k === timeKey) continue;
    spans.push({ text: ' ' }, { text: `${k}=`, role: 'key' }, { text: formatValue(v), role: 'value' });
  }
  return { kind: 'log', level, spans };
};

const tryParseObject = (text: string): Record<string, unknown> | null => {
  const s = text.trim();
  if (!s.startsWith('{') || !s.endsWith('}')) return null;
  try {
    const v: unknown = JSON.parse(s);
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
  } catch {
    return null;
  }
};

// 格式化一行服务输出。receivedAt：后端收到该行的时间（JSON 日志缺 ts 时用它）
export const formatLine = (raw: string, stream: LogStream, receivedAt?: number): FormattedLine => {
  const spans = parseAnsi(raw);
  if (stream === 'system') return { kind: 'system', spans };
  const obj = tryParseObject(spans.map((s) => s.text).join(''));
  if (obj) {
    const log = formatJsonLog(obj, receivedAt);
    if (log) return log;
  }
  return { kind: stream, spans };
};

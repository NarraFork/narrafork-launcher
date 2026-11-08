// LauncherService：后端 internal/services/launcher 的前端封装。
// ViewModel 只经此处访问后端，不直接 import bindings / runtime。
import { Events } from '@wailsio/runtime';
import * as Binding from '@bindings/narrafork-launcher/internal/services/launcher/service';

export type LauncherState = 'stopped' | 'starting' | 'running' | 'stopping' | 'error';
export type OpenMode = 'inline' | 'external' | 'none';
export type CloseAction = 'minimize' | 'quit';
export type Channel = 'stable' | 'beta';
export type DownloadSource = 'auto' | 'github' | 'mirror';
/** 实际使用的源（DownloadSource 去掉 auto）。空串 = 尚未请求过。 */
export type ActiveSource = 'github' | 'mirror' | '';
export type DownloadProxyMode = 'system' | 'direct' | 'custom';
export type NarraforkProxyMode = 'inherit' | 'none' | 'system' | 'custom' | 'download';
/** 后端 NetError.Kind；界面据此翻译。 */
export type NetErrorKind =
  | 'dns'
  | 'timeout'
  | 'refused'
  | 'reset'
  | 'tls'
  | 'proxy'
  | 'network'
  | 'stalled'
  | 'http'
  | 'rateLimit'
  | 'badResponse'
  | 'checksum'
  | 'config'
  | 'cancelled';
export type ConfigErrorKind = 'invalidPort' | 'invalidHost' | 'invalidMirrorUrl' | 'invalidProxyUrl';

export interface LauncherStatus {
  state: LauncherState;
  pid: number;
  url: string;
  version: string;
  installed: string;
  binaryPath: string;
  startedAt: number;
  error: string;
  downloading: boolean;
  preparing: boolean;
}

export interface LauncherConfig {
  port: number;
  host: string;
  autoStart: boolean;
  autoUpdate: boolean;
  channel: Channel;
  openMode: OpenMode;
  openOnReady: boolean;
  closeAction: CloseAction;
  binaryPath: string;
  downloadSource: DownloadSource;
  mirrorUrl: string;
  downloadProxyMode: DownloadProxyMode;
  downloadProxyUrl: string;
  narraforkProxyMode: NarraforkProxyMode;
  narraforkProxyUrl: string;
}

export interface NetworkStatus {
  downloadSource: DownloadSource;
  activeSource: ActiveSource;
  lastSource: ActiveSource;
  mirrorUrl: string;
  proxyMode: DownloadProxyMode;
  /** 下载实际使用的代理（密码已隐藏）；直连为空。 */
  proxy: string;
  proxyOrigin: 'env' | 'registry' | 'custom' | '';
  proxyError: string;
  systemProxy: string;
  systemProxyOrigin: 'env' | 'registry' | '';
}

export type NetworkTestId = 'githubApi' | 'githubDownload' | 'mirror' | 'proxy';

export interface NetworkTestResult {
  id: NetworkTestId;
  url: string;
  proxy: string;
  ok: boolean;
  skipped: boolean;
  status: number;
  latencyMs: number;
  error: string;
  errorKind: NetErrorKind | '';
}

/** 后端返回错误的结构化信息（Wails RuntimeError.cause）。 */
export interface LauncherError {
  message: string;
  /** NetError.Kind 或 ConfigError.Kind；未知错误为空。 */
  kind: NetErrorKind | ConfigErrorKind | '';
  op: 'api' | 'checksum' | 'download' | 'test' | '';
  source: ActiveSource;
  host: string;
  status: number;
  field: string;
}

export interface LogLine {
  seq: number;
  stream: 'stdout' | 'stderr' | 'system';
  text: string;
  time: number;
}

export interface ReleaseInfo {
  version: string;
  tag: string;
  name: string;
  prerelease: boolean;
  publishedAt: string;
  notes: string;
  htmlUrl: string;
  assetName: string;
  assetSize: number;
}

export interface UpdateCheck {
  current: string;
  latest: ReleaseInfo | null;
  available: boolean;
  platform: string;
  /** 发布信息来自哪个源。 */
  source: ActiveSource;
  /** 是否命中后端 5 分钟缓存。 */
  cached: boolean;
}

export interface DownloadProgress {
  version: string;
  received: number;
  total: number;
  phase: 'downloading' | 'retrying' | 'verifying' | 'done' | 'error' | 'cancelled';
  error: string;
  bytesPerSec: number;
  source: ActiveSource;
  host: string;
  attempt: number;
  resumed: boolean;
  errorKind: NetErrorKind | '';
}

export interface Dependency {
  name: string;
  required: boolean;
  installed: boolean;
  version: string;
  path: string;
  installUrl: string;
}

export interface GitIdentity {
  name: string;
  email: string;
}

export const EMPTY_STATUS: LauncherStatus = {
  state: 'stopped',
  pid: 0,
  url: '',
  version: '',
  installed: '',
  binaryPath: '',
  startedAt: 0,
  error: '',
  downloading: false,
  preparing: false,
};

const on = <T>(name: string, handler: (data: T) => void): (() => void) =>
  Events.On(name, (e: { data: unknown }) => handler(e.data as T));

const normRelease = (r: Partial<ReleaseInfo> | null | undefined): ReleaseInfo | null =>
  r ? ({ ...r } as ReleaseInfo) : null;

const str = (v: unknown): string => (typeof v === 'string' ? v : '');

/**
 * 把后端调用抛出的错误整理成 LauncherError。Wails 把 Go 返回的 error 序列化后放在 RuntimeError.cause：
 * NetError / ConfigError 带 kind 等字段，其它错误 cause 只是字符串或空对象。
 */
export const toLauncherError = (e: unknown): LauncherError => {
  const message = e instanceof Error ? e.message : String(e);
  const raw = e instanceof Error ? (e as Error & { cause?: unknown }).cause : undefined;
  const cause = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : {};
  return {
    message: str(cause.message) || message,
    kind: str(cause.kind) as LauncherError['kind'],
    op: str(cause.op) as LauncherError['op'],
    source: str(cause.source) as ActiveSource,
    host: str(cause.host),
    status: typeof cause.status === 'number' ? cause.status : 0,
    field: str(cause.field),
  };
};

export const LauncherService = {
  async getStatus(): Promise<LauncherStatus> {
    return { ...EMPTY_STATUS, ...(await Binding.GetStatus()) } as LauncherStatus;
  },
  onStatus: (handler: (s: LauncherStatus) => void) =>
    on<LauncherStatus>('launcher:status', (s) => handler({ ...EMPTY_STATUS, ...s })),

  async getLogs(): Promise<LogLine[]> {
    return ((await Binding.GetLogs()) ?? []) as LogLine[];
  },
  clearLogs: (): Promise<void> => Binding.ClearLogs(),
  onLog: (handler: (lines: LogLine[]) => void) => on<LogLine[]>('launcher:log', (l) => handler(l ?? [])),

  start: (): Promise<void> => Binding.Start(),
  stop: (): Promise<void> => Binding.Stop(),
  restart: (): Promise<void> => Binding.Restart(),
  open: (): Promise<void> => Binding.Open(),
  openExternal: (): Promise<void> => Binding.OpenExternal(),

  async getConfig(): Promise<LauncherConfig> {
    return (await Binding.GetConfig()) as LauncherConfig;
  },
  async saveConfig(cfg: LauncherConfig): Promise<LauncherConfig> {
    return (await Binding.SaveConfig(cfg)) as LauncherConfig;
  },

  async checkUpdate(): Promise<UpdateCheck> {
    const r = await Binding.CheckUpdate();
    return { ...r, latest: normRelease(r.latest) } as UpdateCheck;
  },
  async listReleases(): Promise<ReleaseInfo[]> {
    return ((await Binding.ListReleases()) ?? []).map((r) => normRelease(r)!) as ReleaseInfo[];
  },
  install: (version = ''): Promise<unknown> => Binding.Install(version),
  cancelDownload: (): Promise<void> => Binding.CancelDownload(),
  onDownload: (handler: (p: DownloadProgress) => void) => on<DownloadProgress>('launcher:download', handler),
  dataDir: (): Promise<string> => Binding.DataDir(),
  revealDownloads: (): Promise<void> => Binding.RevealDownloads(),

  async getNetworkStatus(): Promise<NetworkStatus> {
    return (await Binding.GetNetworkStatus()) as NetworkStatus;
  },
  async testNetwork(): Promise<NetworkTestResult[]> {
    return ((await Binding.TestNetwork()) ?? []) as NetworkTestResult[];
  },

  clearBrowsingData: (): Promise<void> => Binding.ClearBrowsingData(),
  resetWebviewData: (): Promise<void> => Binding.ResetWebviewData(),

  async checkDependencies(): Promise<Dependency[]> {
    return ((await Binding.CheckDependencies()) ?? []) as Dependency[];
  },
  async getGitIdentity(): Promise<GitIdentity> {
    return (await Binding.GetGitIdentity()) as GitIdentity;
  },
  setGitIdentity: (id: GitIdentity): Promise<void> => Binding.SetGitIdentity(id),
  openURL: (url: string): Promise<void> => Binding.OpenURL(url),
} as const;

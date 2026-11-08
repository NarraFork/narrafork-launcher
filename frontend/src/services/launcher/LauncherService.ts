// LauncherService：后端 internal/services/launcher 的前端封装。
// ViewModel 只经此处访问后端，不直接 import bindings / runtime。
import { Events } from '@wailsio/runtime';
import * as Binding from '@bindings/narrafork-launcher/internal/services/launcher/service';

export type LauncherState = 'stopped' | 'starting' | 'running' | 'stopping' | 'error';
export type OpenMode = 'inline' | 'external' | 'none';
export type CloseAction = 'minimize' | 'quit';
export type Channel = 'stable' | 'beta';

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
}

export interface DownloadProgress {
  version: string;
  received: number;
  total: number;
  phase: 'downloading' | 'verifying' | 'done' | 'error' | 'cancelled';
  error: string;
  bytesPerSec: number;
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

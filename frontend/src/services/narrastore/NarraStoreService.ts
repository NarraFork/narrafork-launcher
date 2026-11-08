// NarraStoreService：后端 internal/services/narrastore 的前端封装（NarraFork 数据目录的统计 / 删除 / 切换）。
// ViewModel 只经此处访问后端，不直接 import bindings。
import {
  Service as Binding,
  type ScanResult as RawScanResult,
} from '@bindings/narrafork-launcher/internal/services/narrastore';

export type StorageRisk = 'safe' | 'caution' | 'danger';

// Delete 的失败原因代码（与后端 ErrCode* 对齐）；前端映射为 storage.deleteError.<code>。
export type DeleteErrorCode = '' | 'unknown' | 'notDeletable' | 'running' | 'confirmRequired' | 'failed' | 'cancelled';

// CheckHome 的失败原因代码；前端映射为 storage.home.invalid.<code>。
export type HomeCheckError = '' | 'notAbsolute' | 'isFile' | 'isRoot' | 'isUserHome' | 'notCreatable';

export interface HomeInfo {
  path: string;
  exists: boolean;
  custom: boolean;
  defaultPath: string;
  defaultFromEnv: boolean;
}

export interface HomeCheck {
  path: string;
  valid: boolean;
  error: HomeCheckError;
  exists: boolean;
  empty: boolean;
  looksLikeHome: boolean;
  isDefault: boolean;
}

export interface RunState {
  running: boolean;
  managed: boolean;
  lockPid: number;
}

// labelKey / descriptionKey 是完整 i18n key（storage.categories.<id>.label / .description）。
export interface StorageCategory {
  id: string;
  labelKey: string;
  descriptionKey: string;
  risk: StorageRisk;
  requiresStopped: boolean;
  deletable: boolean;
  bytes: number;
  files: number;
  paths: string[];
  morePaths: number;
}

export interface ScanResult {
  home: string;
  exists: boolean;
  totalBytes: number;
  totalFiles: number;
  categories: StorageCategory[];
  truncated: boolean;
  skipped: number;
  scannedAt: number;
  durationMs: number;
  run: RunState;
}

export interface DeleteResult {
  id: string;
  ok: boolean;
  error: DeleteErrorCode;
  message: string;
  entries: number;
  removedFiles: number;
  freedBytes: number;
}

export interface BackupResult {
  dir: string;
  files: string[];
  bytes: number;
}

// 可取消的扫描句柄：cancel() 会取消后端 context，Go 侧遍历随即停止。
export interface ScanHandle {
  result: Promise<ScanResult>;
  cancel: () => void;
}

const normHome = (h: Partial<HomeInfo> | null | undefined): HomeInfo => ({
  path: h?.path ?? '',
  exists: Boolean(h?.exists),
  custom: Boolean(h?.custom),
  defaultPath: h?.defaultPath ?? '',
  defaultFromEnv: Boolean(h?.defaultFromEnv),
});

const normRun = (r: Partial<RunState> | null | undefined): RunState => ({
  running: Boolean(r?.running),
  managed: Boolean(r?.managed),
  lockPid: Number(r?.lockPid ?? 0),
});

const normRisk = (r: unknown): StorageRisk => (r === 'safe' || r === 'caution' ? r : 'danger');

const normScan = (s: RawScanResult): ScanResult => ({
  home: s.home ?? '',
  exists: Boolean(s.exists),
  totalBytes: Number(s.totalBytes ?? 0),
  totalFiles: Number(s.totalFiles ?? 0),
  categories: (s.categories ?? []).map<StorageCategory>((c) => ({
    id: c.id ?? '',
    labelKey: c.labelKey ?? '',
    descriptionKey: c.descriptionKey ?? '',
    risk: normRisk(c.risk),
    requiresStopped: Boolean(c.requiresStopped),
    deletable: Boolean(c.deletable),
    bytes: Number(c.bytes ?? 0),
    files: Number(c.files ?? 0),
    paths: c.paths ?? [],
    morePaths: Number(c.morePaths ?? 0),
  })),
  truncated: Boolean(s.truncated),
  skipped: Number(s.skipped ?? 0),
  scannedAt: Number(s.scannedAt ?? 0),
  durationMs: Number(s.durationMs ?? 0),
  run: normRun(s.run),
});

export const NarraStoreService = {
  async getHome(): Promise<HomeInfo> {
    return normHome(await Binding.GetHome());
  },

  async checkHome(path: string): Promise<HomeCheck> {
    const c = await Binding.CheckHome(path);
    return {
      path: c.path ?? '',
      valid: Boolean(c.valid),
      error: (c.error ?? '') as HomeCheckError,
      exists: Boolean(c.exists),
      empty: Boolean(c.empty),
      looksLikeHome: Boolean(c.looksLikeHome),
      isDefault: Boolean(c.isDefault),
    };
  },

  async setHome(path: string): Promise<HomeInfo> {
    return normHome(await Binding.SetHome(path));
  },

  async resetHome(): Promise<HomeInfo> {
    return normHome(await Binding.ResetHome());
  },

  revealHome: (): Promise<void> => Binding.RevealHome(),

  async getRunState(): Promise<RunState> {
    return normRun(await Binding.GetRunState());
  },

  scan(): ScanHandle {
    const call = Binding.Scan();
    return {
      result: call.then((s) => normScan(s)),
      cancel: () => call.cancel(),
    };
  },

  // confirmDanger 只能在用户确认过危险操作后传 true。
  async delete(ids: string[], confirmDanger: boolean): Promise<DeleteResult[]> {
    const rs = (await Binding.Delete(ids, confirmDanger)) ?? [];
    return rs.map<DeleteResult>((r) => ({
      id: r.id ?? '',
      ok: Boolean(r.ok),
      error: (r.error ?? '') as DeleteErrorCode,
      message: r.message ?? '',
      entries: Number(r.entries ?? 0),
      removedFiles: Number(r.removedFiles ?? 0),
      freedBytes: Number(r.freedBytes ?? 0),
    }));
  },

  async backupDatabase(destDir: string): Promise<BackupResult> {
    const r = await Binding.BackupDatabase(destDir);
    return { dir: r.dir ?? '', files: r.files ?? [], bytes: Number(r.bytes ?? 0) };
  },
} as const;

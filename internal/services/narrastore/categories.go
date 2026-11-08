package narrastore

import (
	"regexp"
	"strings"
)

// 风险分级：
//   - safe：可再生的缓存 / 日志 / 临时文件，删除后 NarraFork 会按需重建
//   - caution：删除会丢失可恢复性（改动历史、分享链接、未完成的更新等），但不是核心用户数据
//   - danger：用户数据或配置（数据库、凭据、附件、插件……），删除不可恢复
const (
	RiskSafe    = "safe"
	RiskCaution = "caution"
	RiskDanger  = "danger"
)

const (
	// categoryInternal 是 NarraFork 的运行 / 恢复状态（锁、迁移日志、恢复清单），只统计不删除。
	categoryInternal = "internal"
	// categoryOther 收纳分类表未覆盖的条目，只统计不删除。
	categoryOther = "other"
)

// categoryDef 描述一个分类。paths 为相对数据目录的精确路径（正斜杠），命中即整棵子树归入该分类；
// patterns 只匹配数据目录根下的文件名。
//
// 路径均取自 NarraFork 源码（server/ 下），注释给出出处。新增 NarraFork 数据时同步这里；
// 漏掉的条目会落进 other（只统计、不可删除），不会被误删。
type categoryDef struct {
	id              string
	risk            string
	requiresStopped bool
	deletable       bool
	paths           []string
	patterns        []*regexp.Regexp
}

// categoryDefs 的顺序即界面展示顺序。
var categoryDefs = []categoryDef{
	{
		// lib/logger.ts:82-101 轮转出的 server.log.1..N；manager/main.ts:346-355 的 manager.log.1。
		// 进程只写当前日志，轮转文件随时可删。
		id: "rotatedLogs", risk: RiskSafe, requiresStopped: false, deletable: true,
		patterns: []*regexp.Regexp{regexp.MustCompile(`^(server|manager)\.log\.\d+$`)},
	},
	{
		// lib/logger.ts:9 server.log；manager/main.ts:346 manager.log。运行中被持续追加写入。
		id: "logs", risk: RiskSafe, requiresStopped: true, deletable: true,
		paths: []string{"server.log", "manager.log"},
	},
	{
		// lib/request-recorder-protocol.ts:8 滚动请求录制；services/legacy-request-dump-cleanup.ts:10-14
		// 旧版请求转储（NarraFork 启动时自己也会删）。
		id: "requestRecordings", risk: RiskSafe, requiresStopped: true, deletable: true,
		paths: []string{"request-recordings", "request-dumps", "malformed-request-dumps", "token-repair-jobs"},
	},
	{
		// 模型列表 / 额度缓存：lib/kimi-usage-cache.ts:68、lib/custom-api-quota-cache.ts:14、
		// lib/nug-model-cache.ts:9、routes/nug.ts:44、routes/anthropic.ts:32、routes/gemini.ts:52、
		// routes/openai.ts:20,36、lib/deepseek-balance-cache.ts:65、lib/opencode-usage-cache.ts:134、
		// lib/provider-balance-cache.ts:396；tree-sitter 语法缓存 lib/agent/structural/grammar-store.ts:37。
		id: "caches", risk: RiskSafe, requiresStopped: true, deletable: true,
		paths: []string{
			"grammars",
			"kimi-usages.json", "custom-api-quotas.json", "nug-quotas.json", "nug-models-providers.json",
			"anthropic-models-providers.json", "gemini-models-providers.json",
			"openai-models.json", "openai-models-providers.json",
			"deepseek-balances.json", "opencode-usages.json", "provider-balances.json",
		},
	},
	{
		// lib/helper-binaries.ts:33 bin/（rg / zstd / 远程执行器），lib/helper-distribution-runtime.ts:21
		// bin/distribution 下载缓存。按需重新下载；运行中可执行文件可能被占用。
		id: "helperBinaries", risk: RiskSafe, requiresStopped: true, deletable: true,
		paths: []string{"bin"},
	},
	{
		// 临时 / 运行期文件：lib/pack-archives.ts:26 packs（知识包解压目录，激活时重建）、
		// terminal/buffer-manager.ts:30 buffers（终端回滚缓存）、services/tool-input-stream-source.ts:326、
		// lib/browser/memory-profiler.ts:667、gateway/platforms/weixin.ts:927 weixin/tmp、
		// main.ts:1713 snapshots（旧版快照，NarraFork 启动时自己会删）。
		id: "temp", risk: RiskSafe, requiresStopped: true, deletable: true,
		paths: []string{"packs", "buffers", "tool-input-streams", "memory-profiles", "weixin/tmp", "snapshots"},
	},
	{
		// services/update-service.ts:235-239 updates/：自更新下载与暂存；其中 placed-update.json /
		// planned-update-recovery.json 是进行中更新的恢复状态。
		id: "updates", risk: RiskCaution, requiresStopped: true, deletable: true,
		paths: []string{"updates"},
	},
	{
		// lib/model-catalog/index.ts:72-76 model-catalog/：下载的模型目录 + 用户的 overrides.json。
		id: "modelCatalog", risk: RiskCaution, requiresStopped: true, deletable: true,
		paths: []string{"model-catalog"},
	},
	{
		// services/narrator-buffer.ts:282 buffered-files（排队消息的附件）、
		// lib/attachment-staging.ts:73 attachment-staging（尚未发送的附件，6 小时 TTL）。
		id: "staging", risk: RiskCaution, requiresStopped: true, deletable: true,
		paths: []string{"buffered-files", "attachment-staging"},
	},
	{
		// services/file-change-blob-store.ts:118 file-change-blobs（文件改动历史，回退用）、
		// services/worktree-tree-snapshot.ts:84 tree-snapshots（工作区树快照）。
		id: "history", risk: RiskCaution, requiresStopped: true, deletable: true,
		paths: []string{"file-change-blobs", "tree-snapshots"},
	},
	{
		// lib/shares.ts:9 shares/：分享链接的内容，删除后链接失效。
		id: "shares", risk: RiskCaution, requiresStopped: true, deletable: true,
		paths: []string{"shares"},
	},
	{
		// lib/request-recorder-protocol.ts:9 saved-request-recordings：手动保存的请求录制。
		id: "savedRecordings", risk: RiskCaution, requiresStopped: true, deletable: true,
		paths: []string{"saved-request-recordings"},
	},
	{
		// services/device-transfer-service.ts:92-94 transfers/：与远程设备之间传输的文件
		// （settings.devices.transfersDir 可改到别处，那时不在数据目录内）。
		id: "transfers", risk: RiskCaution, requiresStopped: true, deletable: true,
		paths: []string{"transfers"},
	},
	{
		// lib/uploads.ts:48 uploads（会话图片 + avatars）、lib/chat-attachments.ts:78、
		// lib/agent/image-generation.ts:5,26 generated_images。会话历史引用这些文件。
		id: "attachments", risk: RiskDanger, requiresStopped: true, deletable: true,
		paths: []string{"uploads", "chat-attachments", "generated_images"},
	},
	{
		// 用户编写 / 导入的内容：services/skill-service.ts:1148 skills、
		// services/custom-subagent-service.ts:38 subagents、services/prompt-library.ts:53、
		// routes/notification-sounds.ts:8、lib/pack-archives.ts:19 pack-archives（知识包归档）。
		id: "userContent", risk: RiskDanger, requiresStopped: true, deletable: true,
		paths: []string{"skills", "subagents", "prompt-library", "notification-sounds", "pack-archives"},
	},
	{
		// services/plugin-package-store.ts:622 等 plugins/（包、状态、权限、密钥库、插件存储）、
		// routes/plugins.ts:666 plugin-imports。
		id: "plugins", risk: RiskDanger, requiresStopped: true, deletable: true,
		paths: []string{"plugins", "plugin-imports"},
	},
	{
		// services/storage-service.ts:72-89 dormant-ignored：旧版章节留下的 git 忽略文件（.env 等），
		// NarraFork 明确不自动删除，由用户决定。
		id: "dormantIgnored", risk: RiskDanger, requiresStopped: true, deletable: true,
		paths: []string{"dormant-ignored"},
	},
	{
		// lib/settings/index.ts:172 settings.json；manager/manager-config.ts:176 manager.json。
		id: "settings", risk: RiskDanger, requiresStopped: true, deletable: true,
		paths: []string{"settings.json", "manager.json"},
	},
	{
		// lib/fs-secret-paths.ts:50-58 列出的凭据文件：lib/secret-store.ts:21 secret.key、
		// lib/provider-keys.ts:27、lib/codex-manager.ts:72-73、update-server(-test).json；
		// services/gen-ui/kit-signature.ts:14、lib/tls.ts:26 tls/、gateway/platforms/weixin.ts:81 weixin/accounts。
		id: "credentials", risk: RiskDanger, requiresStopped: true, deletable: true,
		paths: []string{
			"secret.key", "provider-keys.json", "codex-credentials.json", "codex-stats.json",
			"update-server.json", "update-server-test.json", "gen-ui-kit-signing.key",
			"tls", "weixin/accounts",
		},
	},
	{
		// db/connection.ts:36 narrafork.db 及 SQLite 的 -wal / -shm / -journal。
		// 其它以 narrafork.db 开头的文件（例如用户自己的备份）不归入此类，避免被一并删除。
		id: "database", risk: RiskDanger, requiresStopped: true, deletable: true,
		paths: []string{"narrafork.db", "narrafork.db-wal", "narrafork.db-shm", "narrafork.db-journal"},
	},
	{
		// lib/instance-lock.ts:831-833 narrafork.lock；db/integrity-state.ts:76,263；
		// services/restart-recovery-service.ts:62；services/*-migration-service.ts 的迁移日志；
		// services/file-change-runtime.ts:1633（privateRoot = home，:1400）；
		// services/narrator-worktree-runtime.ts:82-83 worktree-requests(-v2)；
		// lib/fs-secret-paths.ts:71-76 editor-*；lib/server-restart.ts:61；terminal/dtach-service.ts:388。
		id: categoryInternal, risk: RiskDanger, requiresStopped: true, deletable: false,
		paths: []string{
			"narrafork.lock", "db-integrity-state.json", "fts-repair.json", "restart-recovery.json",
			"aggregation-migration.json", "broken-model-migration-undo.json", "provider-prefix-migration.json",
			"file-change-source.json", "worktree-requests", "worktree-requests-v2",
			"editor-transfers", "editor-native", "editor-native-operations", "restart-handoff", "sockets",
		},
		patterns: []*regexp.Regexp{regexp.MustCompile(`^provider-prefix-migration\.unresolved-\d+\.json$`)},
	},
	{
		id: categoryOther, risk: RiskCaution, requiresStopped: true, deletable: false,
	},
}

func findCategory(id string) *categoryDef {
	for i := range categoryDefs {
		if categoryDefs[i].id == id {
			return &categoryDefs[i]
		}
	}
	return nil
}

// classifier 把相对路径映射到分类。
type classifier struct {
	exact      map[string]string // 相对路径 → 分类 id
	containers map[string]bool   // 内部还有更细规则的目录（如 weixin：accounts 与 tmp 分属不同分类）
	patterns   []patternRule
}

type patternRule struct {
	re *regexp.Regexp
	id string
}

var defaultClassifier = newClassifier(categoryDefs)

func newClassifier(defs []categoryDef) *classifier {
	c := &classifier{exact: map[string]string{}, containers: map[string]bool{}}
	for _, d := range defs {
		for _, p := range d.paths {
			c.exact[p] = d.id
			for i := strings.Index(p, "/"); i >= 0; i = nextSlash(p, i) {
				c.containers[p[:i]] = true
			}
		}
		for _, re := range d.patterns {
			c.patterns = append(c.patterns, patternRule{re: re, id: d.id})
		}
	}
	return c
}

func nextSlash(p string, i int) int {
	j := strings.Index(p[i+1:], "/")
	if j < 0 {
		return -1
	}
	return i + 1 + j
}

// classify 返回 rel（相对数据目录、正斜杠）所属分类。descend 为 true 表示 rel 是容器目录，
// 应逐个分类其子项；此时 id 为 other（容器若不是目录，就按未知条目处理）。
func (c *classifier) classify(rel string) (id string, descend bool) {
	if id, ok := c.exact[rel]; ok {
		return id, false
	}
	if c.containers[rel] {
		return categoryOther, true
	}
	if !strings.Contains(rel, "/") {
		for _, p := range c.patterns {
			if p.re.MatchString(rel) {
				return p.id, false
			}
		}
	}
	return categoryOther, false
}

func joinRel(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

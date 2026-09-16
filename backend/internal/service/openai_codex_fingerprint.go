package service

import (
	"net/http"
	"time"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// codexFingerprintIDsContextKey 是暂存在 gin context 的收敛 ID 集合键。
// 由 Forward（非透传）或 forwardOpenAIPassthrough（透传）解析后写入，请求
// 构造器读取用于出站头改写——请求体与出站头必须共享同一份 IDs，保证
// turn_id 等随机字段一致。
const codexFingerprintIDsContextKey = "codex_fingerprint_ids"

// stageCodexFingerprintIDs 将本 attempt 解析出的收敛 ID 暂存到 gin context。
// 必须无条件覆写（含 nil）：failover 从收敛账号切到 off 账号时，上一账号的
// IDs 不得残留并被误应用到新账号的出站头（typed-nil 由应用侧 nil 守卫吸收）。
func stageCodexFingerprintIDs(c *gin.Context, ids *codexFingerprintIDs) {
	if c != nil {
		c.Set(codexFingerprintIDsContextKey, ids)
	}
}

func stagedCodexFingerprintIDs(c *gin.Context, account *Account) *codexFingerprintIDs {
	if c == nil || account == nil || !account.UsesOpenAICodexProtocol() {
		return nil
	}
	value, ok := c.Get(codexFingerprintIDsContextKey)
	if !ok {
		return nil
	}
	ids, ok := value.(*codexFingerprintIDs)
	if !ok || ids == nil {
		return nil
	}
	// Spark 影子与母账号共享凭据；允许其父账号 ID 命中，但仍拒绝其它账号的旧快照。
	if ids.AccountID != account.ID && (account.ParentAccountID == nil || *account.ParentAccountID != ids.AccountID) {
		return nil
	}
	return ids
}

// applyStagedCodexFingerprintHeaders 读取 context 暂存的收敛 ID 并改写出站头。
// 非透传与透传两个请求构造器共用本函数，防止应用语义漂移。仅解析该
// snapshot 的 OAuth 账号可读取，避免 stale context 跨账号 failover 泄漏。
func applyStagedCodexFingerprintHeaders(c *gin.Context, account *Account, h http.Header) {
	applyCodexFingerprintHeaders(h, stagedCodexFingerprintIDs(c, account))
}

func applyStagedCodexFingerprintClientMetadata(c *gin.Context, account *Account, reqBody map[string]any) bool {
	return applyCodexFingerprintClientMetadata(reqBody, stagedCodexFingerprintIDs(c, account))
}

type codexFingerprintMode = acctcore.CodexFingerprintMode

const codexFingerprintOff = acctcore.CodexFingerprintOff
const codexFingerprintDevice = acctcore.CodexFingerprintDevice
const codexFingerprintSession = acctcore.CodexFingerprintSession
const codexFingerprintFull = acctcore.CodexFingerprintFull

const codexFingerprintModeExtraKey = acctcore.CodexFingerprintModeExtraKey
const codexFingerprintSeedExtraKey = acctcore.CodexFingerprintSeedExtraKey

func newCodexFingerprintSeed() string {
	return uuid.NewString()
}

func codexFingerprintModeFromExtra(extra map[string]any) codexFingerprintMode {
	return acctcore.CodexFingerprintModeFromExtra(extra)
}

func codexFingerprintModeRequiresSeed(mode codexFingerprintMode) bool {
	return acctcore.CodexFingerprintModeRequiresSeed(mode)
}

func codexFingerprintSeed(extra map[string]any) (string, bool) {
	return acctcore.CodexFingerprintSeed(extra)
}

func ShouldEnsureCodexFingerprintSeedForExtraUpdates(updates map[string]any) bool {
	return acctcore.ShouldEnsureCodexFingerprintSeedForExtraUpdates(updates)
}

// GetCodexFingerprintMode 从账号 extra JSON 读取指纹收敛模式。
//
// **收敛是显式 opt-in**：未设置、空值或非法值一律按 off 处理，只有管理员
// 明确配置 device / session / full 才收敛。
//
// 历史：v0.1.175（#5553）把缺省值当作 session，导致升级后存量 OAuth 账号
// （普遍没有这个 extra 键）的每个非透传请求都被静默改写 installation /
// session / thread / turn / window 五类标识；#5555、#5556、#5582 报告的额度
// 缩水都卡在该版本边界，并有"回退 v0.1.173 即恢复"与"新账号开收敛后降额"
// 的 A/B 实测。上游的配额判定策略不可观测，因此这里取兼容安全的一侧：
// 不显式 opt-in 就保持 v0.1.175 之前的客户端身份（#5610）。
func (a *Account) GetCodexFingerprintMode() codexFingerprintMode {
	if a == nil || !a.IsOpenAIOAuthLike() {
		return codexFingerprintOff
	}
	return codexFingerprintModeFromExtra(a.Extra)
}

func deriveStableUUIDv4(seed string) string { return nativeopenai.DeriveStableUUIDv4(seed) }

// resolveConvergedInstallationID 返回账号级恒定的 installation_id。
// 优先使用管理员配置的真实 device_id，无则从系统管理的账号随机种子确定性派生。
func resolveConvergedInstallationID(account *Account, seed string) string {
	if account == nil {
		return ""
	}
	if deviceID := account.GetOpenAIDeviceID(); deviceID != "" {
		return deviceID
	}
	if seed == "" {
		return ""
	}
	return deriveStableUUIDv4("sub2api:codex-install-id:v2:" + seed)
}

func resolveConvergedSessionID(seed string) string {
	return nativeopenai.ResolveConvergedSessionID(seed)
}

func resolveConvergedThreadID(seed, clientSessionID string) string {
	return nativeopenai.ResolveConvergedThreadID(seed, clientSessionID)
}

type codexFingerprintIDs = nativeopenai.FingerprintIDs

func resolveCodexFingerprintIDs(account *Account, clientSessionID string, mode codexFingerprintMode) *codexFingerprintIDs {
	if account == nil || mode == codexFingerprintOff {
		return nil
	}
	seed, ok := codexFingerprintSeed(account.Extra)
	if !ok {
		return nil
	}
	return nativeopenai.ResolveFingerprintIDs(account.ID, seed, clientSessionID, string(mode), func(seed string) string { return resolveConvergedInstallationID(account, seed) }, time.Now, func() string { return uuid.Must(uuid.NewV7()).String() })
}

func extractClientSessionID(h http.Header) string { return nativeopenai.ExtractClientSessionID(h) }

// resolveCodexFingerprintIDsFromRequest 从客户端原始请求头中提取 session-id，
// 结合账号配置一次性解析收敛 ID 集合。调用方应将返回的 ids 同时传给
// applyCodexFingerprintHeaders 和 applyCodexFingerprintClientMetadata。
func resolveCodexFingerprintIDsFromRequest(account *Account, clientHeaders http.Header) *codexFingerprintIDs {
	if account == nil {
		return nil
	}
	mode := account.GetCodexFingerprintMode()
	if mode == codexFingerprintOff {
		return nil
	}
	clientSessionID := ""
	if clientHeaders != nil {
		clientSessionID = extractClientSessionID(clientHeaders)
	}
	return resolveCodexFingerprintIDs(account, clientSessionID, mode)
}

func applyCodexFingerprintHeaders(h http.Header, ids *codexFingerprintIDs) {
	nativeopenai.ApplyCodexFingerprintHeaders(h, ids)
}

func applyCodexFingerprintClientMetadata(reqBody map[string]any, ids *codexFingerprintIDs) bool {
	return nativeopenai.ApplyCodexFingerprintClientMetadata(reqBody, ids)
}

func applyCodexFingerprintClientMetadataRaw(body []byte, ids *codexFingerprintIDs) ([]byte, bool, error) {
	return nativeopenai.ApplyCodexFingerprintClientMetadataRaw(body, ids)
}

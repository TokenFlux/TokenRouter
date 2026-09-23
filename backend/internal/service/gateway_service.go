package service

import (
	"log"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	usage "github.com/TokenFlux/TokenRouter/internal/usage"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"

	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/config"
	s09openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/tidwall/gjson"
)

const (
	stickySessionTTL   = time.Hour // 粘性会话TTL
	defaultMaxLineSize = 500 * 1024 * 1024
	// Canonical Claude Code banner. Keep it EXACT (no trailing whitespace/newlines)
	// to match real Claude CLI traffic as closely as possible. When we need a visual
	// separator between system blocks, we add "\n\n" at concatenation time.

	// claudeCodeSystemPromptExpansion 是真实 Claude Code 主系统提示词中"与具体工具无关"
	// 的通用段落（身份/用途总述 + 安全声明 + URL 告警 + Tone and style），逐字取自真实
	// CLI（2.1.x 一致）。伪装路径用它把 system 块数从 2 提升到 3、体量贴近真实 CC，同时
	// 刻意排除 # Doing tasks / # Using your tools / # Executing actions 等会污染被代理
	// 用户行为的工具专属指令。
	usageBillingTimeout                    = 15 * time.Second
	claudeCodeNoopDeltaKeepaliveMinVersion = "2.1.193"
	debugGatewayBodyEnv                    = "SUB2API_DEBUG_GATEWAY_BODY"
	// 上游错误体只需要提取错误 JSON/日志摘要，默认 512KiB 避免错误风暴叠加大请求体。
	gatewayUpstreamErrorBodyReadLimit int64 = 512 << 10
)

const (
	claudeMimicDebugInfoKey = "claude_mimic_debug_info"
)

const (
	cacheTTLTarget5m = "5m"
)

// accountWithLoad 账号与负载信息的组合，用于负载感知调度
type accountWithLoad struct {
	account  *gatewayprovider.ExecutionAccount
	loadInfo *scheduler.AccountLoadInfo
}

var (
	windowCostPrefetchCacheHitTotal  = &billing.SharedWindowCostMetrics().Hit
	windowCostPrefetchCacheMissTotal = &billing.SharedWindowCostMetrics().Miss
	windowCostPrefetchBatchSQLTotal  = &billing.SharedWindowCostMetrics().BatchSQL
	windowCostPrefetchFallbackTotal  = &billing.SharedWindowCostMetrics().Fallback
	windowCostPrefetchErrorTotal     = &billing.SharedWindowCostMetrics().Errors

	// 已废弃：flusher_enabled=true 后不再增长（仅 flag=false 降级直写路径使用）；新主路径见 FlusherMetrics。2026-09 后可移除。
	// userPlatformQuotaDBIncrErrorTotal 统计 finalizePostUsageBilling 异步 goroutine
	// 中 IncrementUsageWithReset 失败次数。Redis 已成功累加 + DB 写失败意味着
	// Redis cache TTL 过期或被清后该笔 cost 会丢失（与实际消费偏差）。
	// oncall 通过 GatewayUserPlatformQuotaIncrStats() 暴露给 ops 面板做阈值告警。
	userPlatformQuotaDBIncrErrorTotal = billing.PlatformQuotaDBIncrErrors()
	// 已废弃：flusher_enabled=true 后不再增长（仅 flag=false 降级直写路径使用）；新主路径见 FlusherMetrics。2026-09 后可移除。
	// userPlatformQuotaDBIncrLegacyErrorTotal 统计 legacy postUsageBilling
	// （applyUsageBilling 在 repo==nil 时 fallback）路径下的失败次数；
	// 与 DB Incr 失败分开计数，便于区分"主路径暂时故障"vs"基础设施长期未配齐"。
	userPlatformQuotaDBIncrLegacyErrorTotal atomic.Int64
	// userPlatformQuotaSentinelSetCacheErrorTotal 统计 checkUserPlatformQuotaEligibility
	// 在 DB 无行时回填 sentinel cache entry 写 Redis 失败的次数（phase A）。
)

func GatewayWindowCostPrefetchStats() (cacheHit, cacheMiss, batchSQL, fallback, errCount int64) {
	return windowCostPrefetchCacheHitTotal.Load(),
		windowCostPrefetchCacheMissTotal.Load(),
		windowCostPrefetchBatchSQLTotal.Load(),
		windowCostPrefetchFallbackTotal.Load(),
		windowCostPrefetchErrorTotal.Load()
}

// GatewayUserPlatformQuotaIncrStats 返回 (mainPathErr, legacyPathErr, sentinelSetErr)。
// mainPathErr：finalizePostUsageBilling 异步 goroutine 写 DB 失败累计次数；
// legacyPathErr：postUsageBilling fallback 路径写 DB 失败累计次数；
// sentinelSetErr：DB 无行时回填 sentinel cache entry 写 Redis 失败累计次数。
// ops 监控面板可以按"持续上升斜率"做告警阈值。
func GatewayUserPlatformQuotaIncrStats() (mainPathErr, legacyPathErr, sentinelSetErr int64) {
	return userPlatformQuotaDBIncrErrorTotal.Load(),
		userPlatformQuotaDBIncrLegacyErrorTotal.Load(),
		billing.SentinelCacheWriteErrors()
}

// GatewayUserPlatformQuotaFlusherStats 暴露 flusher 运行指标供 ops/health 面板查询。
func GatewayUserPlatformQuotaFlusherStats(f *billing.UserPlatformQuotaUsageFlusher) map[string]int64 {
	if f == nil || f.Metrics() == nil {
		return nil
	}
	m := f.Metrics()
	return map[string]int64{
		"flush_success":        m.FlushSuccessTotal.Load(),
		"flush_error":          m.FlushErrorTotal.Load(),
		"flush_batch_size":     m.FlushBatchSizeTotal.Load(),
		"flush_latency_ms_max": m.FlushLatencyMsMax.Load(),
		"dirty_readd":          m.DirtyReaddTotal.Load(),
		"dirty_lost":           m.DirtyLostTotal.Load(),
		"flush_fk_violation":   m.FlushFKViolationTotal.Load(),
	}
}

func openAIStreamEventIsTerminal(data string) bool {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return false
	}
	if trimmed == "[DONE]" {
		return true
	}
	return s09openai.OpenAIStreamEventTypeIsTerminal(gjson.Get(trimmed, "type").String())
}

// openAIStreamEventIsTerminalWithType 复用已提取的 type，避免 SSE 热路径重复扫描 JSON。
func openAIStreamEventIsTerminalWithType(data, eventType string) bool {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return false
	}
	if trimmed == "[DONE]" {
		return true
	}
	return s09openai.OpenAIStreamEventTypeIsTerminal(eventType)
}

func (s *GatewayService) debugModelRoutingEnabled() bool {
	if s == nil {
		return false
	}
	return s.debugModelRouting.Load()
}

func (s *GatewayService) debugClaudeMimicEnabled() bool {
	if s == nil {
		return false
	}
	return s.debugClaudeMimic.Load()
}

func parseDebugEnvBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func shortSessionHash(sessionHash string) string {
	if sessionHash == "" {
		return ""
	}
	if len(sessionHash) <= 8 {
		return sessionHash
	}
	return sessionHash[:8]
}

func redactAuthHeaderValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	// Keep scheme for debugging, redact secret.
	if strings.HasPrefix(strings.ToLower(v), "bearer ") {
		return "Bearer [redacted]"
	}
	return "[redacted]"
}

func safeHeaderValueForLog(key string, v string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "authorization", "x-api-key":
		return redactAuthHeaderValue(v)
	default:
		return strings.TrimSpace(v)
	}
}

func extractSystemPreviewFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	sys := gjson.GetBytes(body, "system")
	if !sys.Exists() {
		return ""
	}

	switch {
	case sys.IsArray():
		for _, item := range sys.Array() {
			if !item.IsObject() {
				continue
			}
			if strings.EqualFold(item.Get("type").String(), "text") {
				if t := item.Get("text").String(); strings.TrimSpace(t) != "" {
					return t
				}
			}
		}
		return ""
	case sys.Type == gjson.String:
		return sys.String()
	default:
		return ""
	}
}

func buildClaudeMimicDebugLine(req *http.Request, body []byte, account *gatewayprovider.ExecutionAccount, tokenType string, mimicClaudeCode bool) string {
	if req == nil {
		return ""
	}

	// Only log a minimal fingerprint to avoid leaking user content.
	interesting := []string{
		"user-agent",
		"x-app",
		"anthropic-dangerous-direct-browser-access",
		"anthropic-version",
		"anthropic-beta",
		"x-stainless-lang",
		"x-stainless-package-version",
		"x-stainless-os",
		"x-stainless-arch",
		"x-stainless-runtime",
		"x-stainless-runtime-version",
		"x-stainless-retry-count",
		"x-stainless-timeout",
		"authorization",
		"x-api-key",
		"content-type",
		"accept",
		"x-stainless-helper-method",
	}

	h := make([]string, 0, len(interesting))
	for _, k := range interesting {
		if v := req.Header.Get(k); v != "" {
			h = append(h, fmt.Sprintf("%s=%q", k, safeHeaderValueForLog(k, v)))
		}
	}

	metaUserID := strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String())
	sysPreview := strings.TrimSpace(extractSystemPreviewFromBody(body))

	// Truncate preview to keep logs sane.
	if len(sysPreview) > 300 {
		sysPreview = sysPreview[:300] + "..."
	}
	sysPreview = strings.ReplaceAll(sysPreview, "\n", "\\n")
	sysPreview = strings.ReplaceAll(sysPreview, "\r", "\\r")

	aid := int64(0)
	aname := ""
	if account != nil {
		aid = account.Record.ID
		aname = account.Record.Name
	}

	return fmt.Sprintf(
		"url=%s account=%d(%s) tokenType=%s mimic=%t meta.user_id=%q system.preview=%q headers={%s}",
		req.URL.String(),
		aid,
		aname,
		tokenType,
		mimicClaudeCode,
		metaUserID,
		sysPreview,
		strings.Join(h, " "),
	)
}

func logClaudeMimicDebug(req *http.Request, body []byte, account *gatewayprovider.ExecutionAccount, tokenType string, mimicClaudeCode bool) {
	line := buildClaudeMimicDebugLine(req, body, account, tokenType, mimicClaudeCode)
	if line == "" {
		return
	}
	logging.LegacyPrintf("service.gateway", "[ClaudeMimicDebug] %s", line)
}

func isClaudeCodeCredentialScopeError(msg string) bool {
	m := strings.ToLower(strings.TrimSpace(msg))
	if m == "" {
		return false
	}
	return strings.Contains(m, "only authorized for use with claude code") &&
		strings.Contains(m, "cannot be used for other api requests")
}

// sseDataRe matches SSE data lines with optional whitespace after colon.
// Some upstream APIs return non-standard "data:" without space (should be "data: ").
var (
	claudeCliUserAgentRe = regexp.MustCompile(`(?i)^claude-cli/\d+\.\d+\.\d+`)

	// claudeCodePromptPrefixes 用于检测 Claude Code 系统提示词的前缀列表
	// 支持多种变体：标准版、Agent SDK 版、Explore Agent 版、Compact 版等
	// 注意：前缀之间不应存在包含关系，否则会导致冗余匹配

)

// ErrNoAvailableAccounts 表示没有可用的账号

var allowedHeaders = claude.AllowedHeaders

// derefGroupID safely dereferences *int64 to int64, returning 0 if nil
func derefGroupID(groupID *int64) int64 {
	if groupID == nil {
		return 0
	}
	return *groupID
}

func prefetchedStickyAccountIDFromContext(ctx context.Context, groupID *int64) int64 {
	prefetchedGroupID, ok := requeststate.PrefetchedStickyGroupIDFromContext(ctx)
	if !ok || prefetchedGroupID != derefGroupID(groupID) {
		return 0
	}
	if accountID, ok := requeststate.PrefetchedStickyAccountIDFromContext(ctx); ok && accountID > 0 {
		return accountID
	}
	return 0
}

// shouldClearStickySession 检查账号是否处于不可调度状态，需要清理粘性会话绑定。
// 委托 IsSchedulable() 判断账号级可调度性（状态、配额、过载、限流等），
// 额外检查模型级限流。
//
// shouldClearStickySession checks if an account is in an unschedulable state
// and the sticky session binding should be cleared.
// Delegates to IsSchedulable() for account-level checks, plus model-level rate limiting.
func shouldClearStickySession(account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	if account == nil {
		return false
	}
	if !account.View().IsSchedulable() {
		return true
	}
	if remaining := gatewayprovider.ExecutionModelPolicy(account).LimitRemaining(context.Background(), requestedModel); remaining > 0 {
		return true
	}
	return false
}

// TempUnscheduleRetryableError 对非池账号的旧版特殊重试错误触发临时封禁。
// 由 handler 层在同账号重试全部用尽、切换账号时调用；池模式只切号不写状态。
func (s *GatewayService) TempUnscheduleRetryableError(ctx context.Context, accountID int64, failoverErr *forwardcore.UpstreamFailoverError) {
	if s == nil || s.accountRepo == nil || failoverErr == nil || !failoverErr.RetryableOnSameAccount {
		return
	}
	// 请求级瞬时故障在切换账号后仍会复现，不能把无关账号逐个摘出调度池。
	if failoverErr.RequestScopedTransient {
		return
	}
	// 池模式重试只控制当前请求的原地重试预算，耗尽后必须直接切号，
	// 不能复用旧版 400/502 特殊错误的一分钟本地冷却。
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		logging.LegacyPrintf("service.gateway", "查询重试耗尽账号失败: account=%d error=%v", accountID, err)
		return
	}
	if account != nil && account.View().IsPoolMode() {
		return
	}
	// 根据状态码选择封禁策略
	switch failoverErr.StatusCode {
	case http.StatusBadRequest:
		accountcore.TempUnscheduleGoogleConfigError(ctx, s.accountRepo, accountID, "[handler]", log.Printf)
	case http.StatusBadGateway:
		accountcore.TempUnscheduleEmptyResponse(ctx, s.accountRepo, accountID, "[handler]", log.Printf)
	}
}

// GatewayService handles API gateway operations
type GatewayService struct {
	freeQuotaGate         *accountcore.FreeQuotaGate
	searchToolsRuntime    *searchtools.Emulator
	nativeAttemptActivity func() (func(), error)
	usageWindowSource     billing.WindowCostSource
	accountRepo           gatewayprovider.ExecutionAccountStore
	groupRepo             routing.GroupRepository

	cache             session.GatewayCache
	digestStore       *session.DigestSessionStore
	cfg               *config.Config
	schedulerSnapshot *scheduler.SnapshotService

	// 用量计费时钟，测试可注入固定时间以覆盖峰值倍率。
	healthObserver *accountprovider.UpstreamHealth

	identityService *claude.RequestFingerprint
	httpUpstream    httpclient.UpstreamTransport

	concurrencyService *scheduler.ConcurrencyService
	messageCredentials *accountcore.MessageCredentialSource
	sessionLimitCache  scheduler.SessionLimitCache // 会话数量限制缓存（仅 Anthropic OAuth/SetupToken）
	windowCostCache    billing.WindowCostCache     // 资金窗口缓存独立于会话登记。
	rpmCache           scheduler.RPMCache          // RPM 计数缓存（仅 Anthropic OAuth/SetupToken）

	completionRecorder *completion.Recorder

	settingService       *gatewayprovider.RuntimeReaders
	responseHeaderFilter *egress.CompiledHeaderFilter
	debugModelRouting    atomic.Bool
	debugClaudeMimic     atomic.Bool
	channelService       *routing.ChannelService
	resolver             *billing.PriceResolver
	debugGatewayBodyFile atomic.Pointer[os.File] // non-nil when SUB2API_DEBUG_GATEWAY_BODY is set
	tlsFPProfileService  *provider.TLSProfiles

	schedulerParameters  *scheduler.Parameters
	advancedAccountStats *scheduler.RuntimeStats
	backgroundTasks      func(string, func()) bool
	usageLogRepo         usage.UsageLogRepository
	deferredService      *accountcore.DeferredService
}

// NewGatewayService creates a new GatewayService
func NewGatewayService(
	accountRepo gatewayprovider.ExecutionAccountStore,
	groupRepo routing.GroupRepository, usageLogRepo usage.UsageLogRepository,

	cache session.GatewayCache,
	cfg *config.Config,
	schedulerSnapshot *scheduler.SnapshotService,
	concurrencyService *scheduler.ConcurrencyService,

	healthObserver *accountprovider.UpstreamHealth,
	identityService *claude.RequestFingerprint,
	httpUpstream httpclient.UpstreamTransport, deferredService *accountcore.DeferredService,

	messageCredentials *accountcore.MessageCredentialSource,
	sessionLimitCache scheduler.SessionLimitCache,
	windowCostCache billing.WindowCostCache,
	rpmCache scheduler.RPMCache,
	digestStore *session.DigestSessionStore,
	settingService *gatewayprovider.RuntimeReaders,
	tlsFPProfileService *provider.TLSProfiles,
	channelService *routing.ChannelService,
	resolver *billing.PriceResolver,

	headerFilter *egress.CompiledHeaderFilter,
) *GatewayService {

	svc := &GatewayService{
		accountRepo: accountRepo,
		groupRepo:   groupRepo,

		cache:              cache,
		digestStore:        digestStore,
		cfg:                cfg,
		schedulerSnapshot:  schedulerSnapshot,
		concurrencyService: concurrencyService,

		healthObserver: healthObserver,

		identityService: identityService,
		httpUpstream:    httpUpstream,

		messageCredentials:   messageCredentials,
		sessionLimitCache:    sessionLimitCache,
		windowCostCache:      windowCostCache,
		rpmCache:             rpmCache,
		settingService:       settingService,
		responseHeaderFilter: headerFilter,
		tlsFPProfileService:  tlsFPProfileService,
		channelService:       channelService,
		resolver:             resolver,
		usageLogRepo:         usageLogRepo,
		deferredService:      deferredService,
	}

	svc.debugModelRouting.Store(parseDebugEnvBool(os.Getenv("SUB2API_DEBUG_MODEL_ROUTING")))
	svc.debugClaudeMimic.Store(parseDebugEnvBool(os.Getenv("SUB2API_DEBUG_CLAUDE_MIMIC")))
	if path := strings.TrimSpace(os.Getenv(debugGatewayBodyEnv)); path != "" {
		svc.initDebugGatewayBodyFile(path)
	}
	return svc
}

// BindStickySession sets session -> account binding with standard TTL.
func (s *GatewayService) BindStickySession(ctx context.Context, groupID *int64, sessionHash string, accountID int64) error {
	if sessionHash == "" || accountID <= 0 || s.cache == nil {
		return nil
	}
	return s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), sessionHash, accountID, stickySessionTTL)
}

// GetCachedSessionAccountID retrieves the account ID bound to a sticky session.
// Returns 0 if no binding exists or on error.
func (s *GatewayService) GetCachedSessionAccountID(ctx context.Context, groupID *int64, sessionHash string) (int64, error) {
	if sessionHash == "" || s.cache == nil {
		return 0, nil
	}
	accountID, err := s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), sessionHash)
	if err != nil {
		return 0, err
	}
	return accountID, nil
}

// FindGeminiSession 查找 Gemini 会话（基于内容摘要链的 Fallback 匹配）
// 返回最长匹配的会话信息（uuid, accountID）
func (s *GatewayService) FindGeminiSession(_ context.Context, groupID int64, prefixHash, digestChain string) (uuid string, accountID int64, matchedChain string, found bool) {
	if digestChain == "" || s.digestStore == nil {
		return "", 0, "", false
	}
	return s.digestStore.Find(groupID, prefixHash, digestChain)
}

// SaveGeminiSession 保存 Gemini 会话。oldDigestChain 为 Find 返回的 matchedChain，用于删旧 key。
func (s *GatewayService) SaveGeminiSession(_ context.Context, groupID int64, prefixHash, digestChain, uuid string, accountID int64, oldDigestChain string) error {
	if digestChain == "" || s.digestStore == nil {
		return nil
	}
	s.digestStore.Save(groupID, prefixHash, digestChain, uuid, accountID, oldDigestChain)
	return nil
}

// FindAnthropicSession 查找 Anthropic 会话（基于内容摘要链的 Fallback 匹配）
func (s *GatewayService) FindAnthropicSession(_ context.Context, groupID int64, prefixHash, digestChain string) (uuid string, accountID int64, matchedChain string, found bool) {
	if digestChain == "" || s.digestStore == nil {
		return "", 0, "", false
	}
	return s.digestStore.Find(groupID, prefixHash, digestChain)
}

// SaveAnthropicSession 保存 Anthropic 会话
func (s *GatewayService) SaveAnthropicSession(_ context.Context, groupID int64, prefixHash, digestChain, uuid string, accountID int64, oldDigestChain string) error {
	if digestChain == "" || s.digestStore == nil {
		return nil
	}
	s.digestStore.Save(groupID, prefixHash, digestChain, uuid, accountID, oldDigestChain)
	return nil
}

const debugGatewayBodyDefaultFilename = "gateway_debug.log"

// initDebugGatewayBodyFile 初始化网关调试日志文件。
//
//   - "1"/"true" 等布尔值 → 当前目录下 gateway_debug.log
//   - 已有目录路径        → 该目录下 gateway_debug.log
//   - 其他               → 视为完整文件路径
func (s *GatewayService) initDebugGatewayBodyFile(path string) {
	if parseDebugEnvBool(path) {
		path = debugGatewayBodyDefaultFilename
	}

	// 如果 path 指向一个已存在的目录，自动追加默认文件名
	//nolint:gosec // 调试日志路径来自管理员环境变量，允许显式指定。
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, debugGatewayBodyDefaultFilename)
	}

	// 确保父目录存在
	if dir := filepath.Dir(path); dir != "." {
		//nolint:gosec // 调试日志路径来自管理员环境变量，允许显式指定。
		if err := os.MkdirAll(dir, 0755); err != nil {
			slog.Error("failed to create gateway debug log directory", "dir", dir, "error", err)
			return
		}
	}

	//nolint:gosec // 调试日志路径来自管理员环境变量，允许显式指定。
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		slog.Error("failed to open gateway debug log file", "path", path, "error", err)
		return
	}
	s.debugGatewayBodyFile.Store(f)
	slog.Info("gateway debug logging enabled", "path", path)
}

// debugLogGatewaySnapshot 将网关请求的完整快照（headers + body）写入独立的调试日志文件，
// 用于对比客户端原始请求和上游转发请求。
//
// 启用方式（环境变量）：
//
//	SUB2API_DEBUG_GATEWAY_BODY=1                          # 写入 gateway_debug.log
//	SUB2API_DEBUG_GATEWAY_BODY=/tmp/gateway_debug.log     # 写入指定路径
//
// tag: "CLIENT_ORIGINAL" 或 "UPSTREAM_FORWARD"
func (s *GatewayService) debugLogGatewaySnapshot(tag string, headers http.Header, body []byte, extra map[string]string) {
	f := s.debugGatewayBodyFile.Load()
	if f == nil {
		return
	}

	var buf strings.Builder
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	fmt.Fprintf(&buf, "\n========== [%s] %s ==========\n", ts, tag)

	// 1. context
	if len(extra) > 0 {
		fmt.Fprint(&buf, "--- context ---\n")
		extraKeys := make([]string, 0, len(extra))
		for k := range extra {
			extraKeys = append(extraKeys, k)
		}
		sort.Strings(extraKeys)
		for _, k := range extraKeys {
			fmt.Fprintf(&buf, "  %s: %s\n", k, extra[k])
		}
	}

	// 2. headers（按真实 Claude CLI wire 顺序排列，便于与抓包对比；auth 脱敏）
	fmt.Fprint(&buf, "--- headers ---\n")
	for _, k := range claude.SortHeadersByWireOrder(headers) {
		for _, v := range headers[k] {
			fmt.Fprintf(&buf, "  %s: %s\n", k, safeHeaderValueForLog(k, v))
		}
	}

	// 3. body（完整输出，格式化 JSON 便于 diff）
	fmt.Fprint(&buf, "--- body ---\n")
	if len(body) == 0 {
		fmt.Fprint(&buf, "  (empty)\n")
	} else {
		var pretty bytes.Buffer
		if json.Indent(&pretty, body, "  ", "  ") == nil {
			fmt.Fprintf(&buf, "  %s\n", pretty.Bytes())
		} else {
			// JSON 格式化失败时原样输出
			fmt.Fprintf(&buf, "  %s\n", body)
		}
	}

	// 写入文件（调试用，并发写入可能交错但不影响可读性）
	_, _ = f.WriteString(buf.String())
}

// BindNativeAttemptActivity 由组合根统一等待已迁原生执行，构造期间绑定且不启动任务。
func (s *GatewayService) BindNativeAttemptActivity(enter func() (func(), error)) {
	s.nativeAttemptActivity = enter
}

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	"github.com/TokenFlux/TokenRouter/internal/util/responseheaders"
	"github.com/cespare/xxhash/v2"
	gocache "github.com/patrickmn/go-cache"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"golang.org/x/sync/singleflight"
)

const (
	claudeAPIURL            = "https://api.anthropic.com/v1/messages?beta=true"
	claudeAPICountTokensURL = "https://api.anthropic.com/v1/messages/count_tokens?beta=true"
	stickySessionTTL        = time.Hour // 粘性会话TTL
	defaultMaxLineSize      = 500 * 1024 * 1024
	// Canonical Claude Code banner. Keep it EXACT (no trailing whitespace/newlines)
	// to match real Claude CLI traffic as closely as possible. When we need a visual
	// separator between system blocks, we add "\n\n" at concatenation time.
	claudeCodeSystemPrompt = "You are Claude Code, Anthropic's official CLI for Claude."
	// claudeCodeSystemPromptExpansion 是真实 Claude Code 主系统提示词中"与具体工具无关"
	// 的通用段落（身份/用途总述 + 安全声明 + URL 告警 + Tone and style），逐字取自真实
	// CLI（2.1.x 一致）。伪装路径用它把 system 块数从 2 提升到 3、体量贴近真实 CC，同时
	// 刻意排除 # Doing tasks / # Using your tools / # Executing actions 等会污染被代理
	// 用户行为的工具专属指令。
	claudeCodeSystemPromptExpansion = `You are an interactive agent that helps users with software engineering tasks. Use the instructions below and the tools available to you to assist the user.

IMPORTANT: Assist with authorized security testing, defensive security, CTF challenges, and educational contexts. Refuse requests for destructive techniques, DoS attacks, mass targeting, supply chain compromise, or detection evasion for malicious purposes. Dual-use security tools (C2 frameworks, credential testing, exploit development) require clear authorization context: pentesting engagements, CTF competitions, security research, or defensive use cases.
IMPORTANT: You must NEVER generate or guess URLs for the user unless you are confident that the URLs are for helping the user with programming. You may use URLs provided by the user in their messages or local files.

# Tone and style
 - Only use emojis if the user explicitly requests it. Avoid using emojis in all communication unless asked.
 - Your responses should be short and concise.
 - When referencing specific functions or pieces of code include the pattern file_path:line_number to allow the user to easily navigate to the source code location.
 - When referencing GitHub issues or pull requests, use the owner/repo#123 format (e.g. anthropics/claude-code#100) so they render as clickable links.
 - Do not use a colon before tool calls. Your tool calls may not be shown directly in the output, so text like "Let me read the file:" followed by a read tool call should just be "Let me read the file." with a period.`
	maxCacheControlBlocks = 4 // Anthropic API 允许的最大 cache_control 块数量

	defaultUserGroupRateCacheTTL           = 30 * time.Second
	defaultModelsListCacheTTL              = 15 * time.Second
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
	cacheTTLTarget1h = "1h"
)

// ForceCacheBillingContextKey 强制缓存计费上下文键
// 用于粘性会话切换时，将 input_tokens 转为 cache_read_input_tokens 计费
type forceCacheBillingKeyType struct{}

// accountWithLoad 账号与负载信息的组合，用于负载感知调度
type accountWithLoad struct {
	account  *Account
	loadInfo *AccountLoadInfo
}

var ForceCacheBillingContextKey = forceCacheBillingKeyType{}

var (
	windowCostPrefetchCacheHitTotal  atomic.Int64
	windowCostPrefetchCacheMissTotal atomic.Int64
	windowCostPrefetchBatchSQLTotal  atomic.Int64
	windowCostPrefetchFallbackTotal  atomic.Int64
	windowCostPrefetchErrorTotal     atomic.Int64

	userGroupRateCacheHitTotal      atomic.Int64
	userGroupRateCacheMissTotal     atomic.Int64
	userGroupRateCacheLoadTotal     atomic.Int64
	userGroupRateCacheSFSharedTotal atomic.Int64
	userGroupRateCacheFallbackTotal atomic.Int64

	modelsListCacheHitTotal   atomic.Int64
	modelsListCacheMissTotal  atomic.Int64
	modelsListCacheStoreTotal atomic.Int64

	// 已废弃：flusher_enabled=true 后不再增长（仅 flag=false 降级直写路径使用）；新主路径见 FlusherMetrics。2026-09 后可移除。
	// userPlatformQuotaDBIncrErrorTotal 统计 finalizePostUsageBilling 异步 goroutine
	// 中 IncrementUsageWithReset 失败次数。Redis 已成功累加 + DB 写失败意味着
	// Redis cache TTL 过期或被清后该笔 cost 会丢失（与实际消费偏差）。
	// oncall 通过 GatewayUserPlatformQuotaIncrStats() 暴露给 ops 面板做阈值告警。
	userPlatformQuotaDBIncrErrorTotal atomic.Int64
	// 已废弃：flusher_enabled=true 后不再增长（仅 flag=false 降级直写路径使用）；新主路径见 FlusherMetrics。2026-09 后可移除。
	// userPlatformQuotaDBIncrLegacyErrorTotal 统计 legacy postUsageBilling
	// （applyUsageBilling 在 repo==nil 时 fallback）路径下的失败次数；
	// 与 DB Incr 失败分开计数，便于区分"主路径暂时故障"vs"基础设施长期未配齐"。
	userPlatformQuotaDBIncrLegacyErrorTotal atomic.Int64
	// userPlatformQuotaSentinelSetCacheErrorTotal 统计 checkUserPlatformQuotaEligibility
	// 在 DB 无行时回填 sentinel cache entry 写 Redis 失败的次数（phase A）。
	userPlatformQuotaSentinelSetCacheErrorTotal atomic.Int64
)

func GatewayWindowCostPrefetchStats() (cacheHit, cacheMiss, batchSQL, fallback, errCount int64) {
	return windowCostPrefetchCacheHitTotal.Load(),
		windowCostPrefetchCacheMissTotal.Load(),
		windowCostPrefetchBatchSQLTotal.Load(),
		windowCostPrefetchFallbackTotal.Load(),
		windowCostPrefetchErrorTotal.Load()
}

func GatewayUserGroupRateCacheStats() (cacheHit, cacheMiss, load, singleflightShared, fallback int64) {
	return userGroupRateCacheHitTotal.Load(),
		userGroupRateCacheMissTotal.Load(),
		userGroupRateCacheLoadTotal.Load(),
		userGroupRateCacheSFSharedTotal.Load(),
		userGroupRateCacheFallbackTotal.Load()
}

func GatewayModelsListCacheStats() (cacheHit, cacheMiss, store int64) {
	return modelsListCacheHitTotal.Load(), modelsListCacheMissTotal.Load(), modelsListCacheStoreTotal.Load()
}

// GatewayUserPlatformQuotaIncrStats 返回 (mainPathErr, legacyPathErr, sentinelSetErr)。
// mainPathErr：finalizePostUsageBilling 异步 goroutine 写 DB 失败累计次数；
// legacyPathErr：postUsageBilling fallback 路径写 DB 失败累计次数；
// sentinelSetErr：DB 无行时回填 sentinel cache entry 写 Redis 失败累计次数。
// ops 监控面板可以按"持续上升斜率"做告警阈值。
func GatewayUserPlatformQuotaIncrStats() (mainPathErr, legacyPathErr, sentinelSetErr int64) {
	return userPlatformQuotaDBIncrErrorTotal.Load(),
		userPlatformQuotaDBIncrLegacyErrorTotal.Load(),
		userPlatformQuotaSentinelSetCacheErrorTotal.Load()
}

// GatewayUserPlatformQuotaFlusherStats 暴露 flusher 运行指标供 ops/health 面板查询。
func GatewayUserPlatformQuotaFlusherStats(f *UserPlatformQuotaUsageFlusher) map[string]int64 {
	if f == nil || f.metrics == nil {
		return nil
	}
	m := f.metrics
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
	return openAIStreamEventTypeIsTerminal(gjson.Get(trimmed, "type").String())
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
	return openAIStreamEventTypeIsTerminal(eventType)
}

func openAIStreamEventTypeIsTerminal(eventType string) bool {
	switch eventType {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		return true
	default:
		return false
	}
}

func anthropicStreamEventIsTerminal(eventName, data string) bool {
	if strings.EqualFold(strings.TrimSpace(eventName), "message_stop") {
		return true
	}
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return false
	}
	if trimmed == "[DONE]" {
		return true
	}
	return gjson.Get(trimmed, "type").String() == "message_stop"
}

func cloneStringSlice(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

// IsForceCacheBilling 检查是否启用强制缓存计费
func IsForceCacheBilling(ctx context.Context) bool {
	v, _ := ctx.Value(ForceCacheBillingContextKey).(bool)
	return v
}

// WithForceCacheBilling 返回带有强制缓存计费标记的上下文
func WithForceCacheBilling(ctx context.Context) context.Context {
	return context.WithValue(ctx, ForceCacheBillingContextKey, true)
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

func buildClaudeMimicDebugLine(req *http.Request, body []byte, account *Account, tokenType string, mimicClaudeCode bool) string {
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
		aid = account.ID
		aname = account.Name
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

func logClaudeMimicDebug(req *http.Request, body []byte, account *Account, tokenType string, mimicClaudeCode bool) {
	line := buildClaudeMimicDebugLine(req, body, account, tokenType, mimicClaudeCode)
	if line == "" {
		return
	}
	logger.LegacyPrintf("service.gateway", "[ClaudeMimicDebug] %s", line)
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
	sseDataRe            = regexp.MustCompile(`^data:\s*`)
	claudeCliUserAgentRe = regexp.MustCompile(`(?i)^claude-cli/\d+\.\d+\.\d+`)

	// claudeCodePromptPrefixes 用于检测 Claude Code 系统提示词的前缀列表
	// 支持多种变体：标准版、Agent SDK 版、Explore Agent 版、Compact 版等
	// 注意：前缀之间不应存在包含关系，否则会导致冗余匹配
	claudeCodePromptPrefixes = []string{
		"You are Claude Code, Anthropic's official CLI for Claude",             // 标准版 & Agent SDK 版（含 running within...）
		"You are a Claude agent, built on Anthropic's Claude Agent SDK",        // Agent SDK 变体
		"You are a file search specialist for Claude Code",                     // Explore Agent 版
		"You are a helpful AI assistant tasked with summarizing conversations", // Compact 版
	}
)

// ErrNoAvailableAccounts 表示没有可用的账号
var ErrNoAvailableAccounts = errors.New("no available accounts")

// ErrClaudeCodeOnly 表示分组仅允许 Claude Code 客户端访问
var ErrClaudeCodeOnly = errors.New("this group only allows Claude Code clients")

// allowedHeaders 白名单headers（参考CRS项目）
var allowedHeaders = map[string]bool{
	"accept":                                    true,
	"x-stainless-retry-count":                   true,
	"x-stainless-timeout":                       true,
	"x-stainless-lang":                          true,
	"x-stainless-package-version":               true,
	"x-stainless-os":                            true,
	"x-stainless-arch":                          true,
	"x-stainless-runtime":                       true,
	"x-stainless-runtime-version":               true,
	"x-stainless-helper-method":                 true,
	"anthropic-dangerous-direct-browser-access": true,
	"anthropic-version":                         true,
	"x-app":                                     true,
	"anthropic-beta":                            true,
	"accept-language":                           true,
	"sec-fetch-mode":                            true,
	"user-agent":                                true,
	"content-type":                              true,
	"accept-encoding":                           true,
	"x-claude-code-session-id":                  true,
	"x-client-request-id":                       true,
}

// GatewayCache 定义网关服务的缓存操作接口。
// 提供粘性会话（Sticky Session）的存储、查询、刷新和删除功能。
//
// GatewayCache defines cache operations for gateway service.
// Provides sticky session storage, retrieval, refresh and deletion capabilities.
type GatewayCache interface {
	// GetSessionAccountID 获取粘性会话绑定的账号 ID
	// Get the account ID bound to a sticky session
	GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error)
	// SetSessionAccountID 设置粘性会话与账号的绑定关系
	// Set the binding between sticky session and account
	SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error
	// RefreshSessionTTL 刷新粘性会话的过期时间
	// Refresh the expiration time of a sticky session
	RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error
	// DeleteSessionAccountID 删除粘性会话绑定，用于账号不可用时主动清理
	// Delete sticky session binding, used to proactively clean up when account becomes unavailable
	DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error
	// SetSessionOwnerGroupID 首次记录显式会话所属分组；返回 true 表示本次写入成功。
	SetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string, groupID int64, ttl time.Duration) (bool, error)
	// GetSessionOwnerGroupID 读取显式会话首次归属分组。
	GetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string) (int64, error)
	// RefreshSessionOwnerTTL 刷新显式会话归属记录的过期时间。
	RefreshSessionOwnerTTL(ctx context.Context, userID int64, source, sessionHash string, ttl time.Duration) error
}

// GrokVideoBillingCache 为异步视频任务保存创建时定价快照，并跨实例防止轮询重复扣费。
// 它保持为独立子接口，避免与 Grok 无关的缓存实现被迫提供这组能力。
type GrokVideoBillingCache interface {
	SetGrokVideoPendingBilling(ctx context.Context, key string, payload []byte, ttl time.Duration) error
	GetGrokVideoPendingBilling(ctx context.Context, key string) ([]byte, error)
	ClaimGrokVideoBilled(ctx context.Context, key string, ttl time.Duration) (bool, error)
	ReleaseGrokVideoBilled(ctx context.Context, key string) error
}

// derefGroupID safely dereferences *int64 to int64, returning 0 if nil
func derefGroupID(groupID *int64) int64 {
	if groupID == nil {
		return 0
	}
	return *groupID
}

func resolveUserGroupRateCacheTTL(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.Gateway.UserGroupRateCacheTTLSeconds <= 0 {
		return defaultUserGroupRateCacheTTL
	}
	return time.Duration(cfg.Gateway.UserGroupRateCacheTTLSeconds) * time.Second
}

func resolveModelsListCacheTTL(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.Gateway.ModelsListCacheTTLSeconds <= 0 {
		return defaultModelsListCacheTTL
	}
	return time.Duration(cfg.Gateway.ModelsListCacheTTLSeconds) * time.Second
}

func modelsListCacheKey(groupID *int64, platform string) string {
	return fmt.Sprintf("%d|%s", derefGroupID(groupID), strings.TrimSpace(platform))
}

func prefetchedStickyGroupIDFromContext(ctx context.Context) (int64, bool) {
	return PrefetchedStickyGroupIDFromContext(ctx)
}

func prefetchedStickyAccountIDFromContext(ctx context.Context, groupID *int64) int64 {
	prefetchedGroupID, ok := prefetchedStickyGroupIDFromContext(ctx)
	if !ok || prefetchedGroupID != derefGroupID(groupID) {
		return 0
	}
	if accountID, ok := PrefetchedStickyAccountIDFromContext(ctx); ok && accountID > 0 {
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
func shouldClearStickySession(account *Account, requestedModel string) bool {
	if account == nil {
		return false
	}
	if !account.IsSchedulable() {
		return true
	}
	if remaining := account.GetRateLimitRemainingTimeWithContext(context.Background(), requestedModel); remaining > 0 {
		return true
	}
	return false
}

type AccountWaitPlan struct {
	AccountID      int64
	MaxConcurrency int
	Timeout        time.Duration
	MaxWaiting     int
}

type AccountSelectionResult struct {
	Account           *Account
	Acquired          bool
	ReleaseFunc       func()
	WaitPlan          *AccountWaitPlan // nil means no wait allowed
	AdvancedScheduler bool             // 仅内部转发链路使用，不对外序列化。
}

// ClaudeUsage 表示Claude API返回的usage信息
type ClaudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreation5mTokens    int // 5分钟缓存创建token（来自嵌套 cache_creation 对象）
	CacheCreation1hTokens    int // 1小时缓存创建token（来自嵌套 cache_creation 对象）
	ImageOutputTokens        int `json:"image_output_tokens,omitempty"`
	// Speed 记录 Claude 实际返回的处理速度，"fast" 会映射到内部 priority 计费。
	Speed string `json:"speed,omitempty"`
}

// ForwardResult 转发结果
type AudioUsage struct {
	Mode            string  // realtime | tts | stt
	DurationOrUnits float64 // minutes / million-chars / hours
}

type ForwardResult struct {
	RequestID string
	Usage     ClaudeUsage
	Model     string
	// UpstreamModel is the actual upstream model after mapping.
	// Prefer empty when it is identical to Model; persistence normalizes equal values away as no-op mappings.
	UpstreamModel    string
	Stream           bool
	Duration         time.Duration
	FirstTokenMs     *int // 首字时间（流式请求）
	ClientDisconnect bool // 客户端是否在流式传输过程中断开
	ReasoningEffort  *string
	ResponseBody     []byte // 成功响应体，用于数据共享提取 assistant 输出

	// 图片生成计费字段（图片生成模型使用）
	ImageCount         int    // 生成的图片数量
	ImageSize          string // 最终计费尺寸 "1K", "2K", "4K"
	ImageInputSize     string // 请求中的原始图片尺寸
	ImageOutputSize    string // 上游响应中的图片尺寸
	ImageOutputSizes   []string
	ImageSizeSource    string
	ImageSizeBreakdown map[string]int
	SearchCount        int
	AudioUsage         *AudioUsage
}

// GatewayFailureStage 标识请求失败的阶段。零值有意按推理阶段处理，
// 从而保持现有 UpstreamFailoverError 调用方的行为。
type GatewayFailureStage string

const (
	GatewayFailureStageInference   GatewayFailureStage = "inference"
	GatewayFailureStageAccountAuth GatewayFailureStage = "account_auth"
)

// GatewayFailureScope 标识切换账号是否可能解决当前失败。
type GatewayFailureScope string

const (
	GatewayFailureScopeAccount  GatewayFailureScope = "account"
	GatewayFailureScopeProvider GatewayFailureScope = "provider"
	GatewayFailureScopeRequest  GatewayFailureScope = "request"
)

// NextAccountAction 为保持向后兼容采用三态值。零值表示旧版重试行为，
// 只有 NextAccountStop 会显式终止账号切换。
type NextAccountAction uint8

const (
	NextAccountLegacyRetry NextAccountAction = iota
	NextAccountRetry
	NextAccountStop
)

type GatewayFailureReason string

// UpstreamFailoverError 表示可能触发账号切换的上游或凭据错误。
// 新增元数据保持现有复合字面量源码兼容，并保留旧版切换账号行为。
type UpstreamFailoverError struct {
	StatusCode               int
	ResponseBody             []byte      // 上游响应体，用于错误透传规则匹配
	ResponseHeaders          http.Header // 上游响应头，用于透传 cf-ray/cf-mitigated/content-type 等诊断信息
	ForceCacheBilling        bool        // Antigravity 粘性会话切换时设为 true
	RetryableOnSameAccount   bool        // 临时性错误（如 Google 间歇性 400、空响应），应在同一账号上重试 N 次再切换
	RequestScopedTransient   bool        // 请求级瞬时故障与账号健康无关：可同账号重试，但不得据此临时封禁账号
	SafeToFailoverAfterWrite bool        // 仅写出 SSE 注释等非语义字节时，仍可在同一客户端流中切换账号
	Stage                    GatewayFailureStage
	Scope                    GatewayFailureScope
	Reason                   GatewayFailureReason
	NextAccountAction        NextAccountAction
	ClientStatusCode         int
	ClientMessage            string
}

func (e *UpstreamFailoverError) Error() string {
	if e != nil && e.Stage == GatewayFailureStageAccountAuth {
		return fmt.Sprintf("credential failure: %s (failover)", e.Reason)
	}
	return fmt.Sprintf("upstream error: %d (failover)", e.StatusCode)
}

func (e *UpstreamFailoverError) ShouldRetryNextAccount() bool {
	return e != nil && e.NextAccountAction != NextAccountStop
}

func (e *UpstreamFailoverError) IsCredentialFailure() bool {
	return e != nil && e.Stage == GatewayFailureStageAccountAuth
}

// ShouldReportAccountScheduleFailure 防止把提供方级或请求级凭据失败误归因到当前账号。
// 旧版错误和推理错误继续保持原有的调度健康上报行为。
func (e *UpstreamFailoverError) ShouldReportAccountScheduleFailure() bool {
	if e == nil {
		return false
	}
	return !e.IsCredentialFailure() || e.Scope == GatewayFailureScopeAccount
}

// sseStreamErrorEventError 表示上游 SSE 流体内出现 event:error 帧。
// RawData 保留该事件 data: 行的原始内容，供上层写入 failover body 和 Ops 日志。
type sseStreamErrorEventError struct {
	RawData string
}

func (e *sseStreamErrorEventError) Error() string { return "have error in stream" }

// TempUnscheduleRetryableError 对非池账号的旧版特殊重试错误触发临时封禁。
// 由 handler 层在同账号重试全部用尽、切换账号时调用；池模式只切号不写状态。
func (s *GatewayService) TempUnscheduleRetryableError(ctx context.Context, accountID int64, failoverErr *UpstreamFailoverError) {
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
		logger.LegacyPrintf("service.gateway", "查询重试耗尽账号失败: account=%d error=%v", accountID, err)
		return
	}
	if account != nil && account.IsPoolMode() {
		return
	}
	// 根据状态码选择封禁策略
	switch failoverErr.StatusCode {
	case http.StatusBadRequest:
		tempUnscheduleGoogleConfigError(ctx, s.accountRepo, accountID, "[handler]")
	case http.StatusBadGateway:
		tempUnscheduleEmptyResponse(ctx, s.accountRepo, accountID, "[handler]")
	}
}

// GatewayService handles API gateway operations
type GatewayService struct {
	accountRepo           AccountRepository
	groupRepo             GroupRepository
	usageLogRepo          UsageLogRepository
	usageBillingRepo      UsageBillingRepository
	userRepo              UserRepository
	userSubRepo           UserSubscriptionRepository
	userGroupRateRepo     UserGroupRateRepository
	cache                 GatewayCache
	digestStore           *DigestSessionStore
	cfg                   *config.Config
	schedulerSnapshot     *SchedulerSnapshotService
	billingService        *BillingService
	usageBillingNow       func() time.Time // 用量计费时钟，测试可注入固定时间以覆盖峰值倍率。
	rateLimitService      *RateLimitService
	billingCacheService   *BillingCacheService
	identityService       *IdentityService
	httpUpstream          HTTPUpstream
	deferredService       *DeferredService
	concurrencyService    *ConcurrencyService
	claudeTokenProvider   *ClaudeTokenProvider
	sessionLimitCache     SessionLimitCache // 会话数量限制缓存（仅 Anthropic OAuth/SetupToken）
	rpmCache              RPMCache          // RPM 计数缓存（仅 Anthropic OAuth/SetupToken）
	userGroupRateResolver *userGroupRateResolver
	userGroupRateCache    *gocache.Cache
	userGroupRateSF       singleflight.Group
	modelsListCache       *gocache.Cache
	modelsListCacheTTL    time.Duration
	settingService        *SettingService
	responseHeaderFilter  *responseheaders.CompiledHeaderFilter
	debugModelRouting     atomic.Bool
	debugClaudeMimic      atomic.Bool
	channelService        *ChannelService
	resolver              *ModelPricingResolver
	debugGatewayBodyFile  atomic.Pointer[os.File] // non-nil when SUB2API_DEBUG_GATEWAY_BODY is set
	tlsFPProfileService   *TLSFingerprintProfileService
	balanceNotifyService  *BalanceNotifyService
	userPlatformQuotaRepo UserPlatformQuotaRepository
	dataSharingService    *DataSharingService
	advancedAccountStats  *advancedAccountRuntimeStats
}

// NewGatewayService creates a new GatewayService
func NewGatewayService(
	accountRepo AccountRepository,
	groupRepo GroupRepository,
	usageLogRepo UsageLogRepository,
	usageBillingRepo UsageBillingRepository,
	userRepo UserRepository,
	userSubRepo UserSubscriptionRepository,
	userGroupRateRepo UserGroupRateRepository,
	cache GatewayCache,
	cfg *config.Config,
	schedulerSnapshot *SchedulerSnapshotService,
	concurrencyService *ConcurrencyService,
	billingService *BillingService,
	rateLimitService *RateLimitService,
	billingCacheService *BillingCacheService,
	identityService *IdentityService,
	httpUpstream HTTPUpstream,
	deferredService *DeferredService,
	claudeTokenProvider *ClaudeTokenProvider,
	sessionLimitCache SessionLimitCache,
	rpmCache RPMCache,
	digestStore *DigestSessionStore,
	settingService *SettingService,
	tlsFPProfileService *TLSFingerprintProfileService,
	channelService *ChannelService,
	resolver *ModelPricingResolver,
	balanceNotifyService *BalanceNotifyService,
	userPlatformQuotaRepo UserPlatformQuotaRepository,
	dataSharingService *DataSharingService,
) *GatewayService {
	userGroupRateTTL := resolveUserGroupRateCacheTTL(cfg)
	modelsListTTL := resolveModelsListCacheTTL(cfg)

	svc := &GatewayService{
		accountRepo:           accountRepo,
		groupRepo:             groupRepo,
		usageLogRepo:          usageLogRepo,
		usageBillingRepo:      usageBillingRepo,
		userRepo:              userRepo,
		userSubRepo:           userSubRepo,
		userGroupRateRepo:     userGroupRateRepo,
		cache:                 cache,
		digestStore:           digestStore,
		cfg:                   cfg,
		schedulerSnapshot:     schedulerSnapshot,
		concurrencyService:    concurrencyService,
		billingService:        billingService,
		rateLimitService:      rateLimitService,
		billingCacheService:   billingCacheService,
		identityService:       identityService,
		httpUpstream:          httpUpstream,
		deferredService:       deferredService,
		claudeTokenProvider:   claudeTokenProvider,
		sessionLimitCache:     sessionLimitCache,
		rpmCache:              rpmCache,
		userGroupRateCache:    gocache.New(userGroupRateTTL, time.Minute),
		settingService:        settingService,
		modelsListCache:       gocache.New(modelsListTTL, time.Minute),
		modelsListCacheTTL:    modelsListTTL,
		responseHeaderFilter:  compileResponseHeaderFilter(cfg),
		tlsFPProfileService:   tlsFPProfileService,
		channelService:        channelService,
		resolver:              resolver,
		balanceNotifyService:  balanceNotifyService,
		userPlatformQuotaRepo: userPlatformQuotaRepo,
		dataSharingService:    dataSharingService,
		advancedAccountStats:  newAdvancedAccountRuntimeStats(),
	}
	svc.userGroupRateResolver = newUserGroupRateResolver(
		userGroupRateRepo,
		svc.userGroupRateCache,
		userGroupRateTTL,
		&svc.userGroupRateSF,
		"service.gateway",
	)
	svc.debugModelRouting.Store(parseDebugEnvBool(os.Getenv("SUB2API_DEBUG_MODEL_ROUTING")))
	svc.debugClaudeMimic.Store(parseDebugEnvBool(os.Getenv("SUB2API_DEBUG_CLAUDE_MIMIC")))
	if path := strings.TrimSpace(os.Getenv(debugGatewayBodyEnv)); path != "" {
		svc.initDebugGatewayBodyFile(path)
	}
	return svc
}

// GenerateSessionHash 从预解析请求计算粘性会话 hash
func (s *GatewayService) GenerateSessionHash(parsed *ParsedRequest) string {
	if parsed == nil {
		return ""
	}

	// 1. 最高优先级：从 metadata.user_id 提取 session_xxx
	if parsed.MetadataUserID != "" {
		uid := ParseMetadataUserID(parsed.MetadataUserID)
		if uid != nil && uid.SessionID != "" {
			slog.Info("sticky.hash_source",
				"source", "metadata_user_id",
				"session_id", uid.SessionID,
				"device_id", uid.DeviceID,
				"is_new_format", uid.IsNewFormat,
			)
			return uid.SessionID
		}
		slog.Info("sticky.hash_metadata_parse_failed",
			"metadata_user_id", parsed.MetadataUserID,
			"parsed_nil", uid == nil,
		)
	}

	// 2. 提取带 cache_control: {type: "ephemeral"} 的内容
	cacheableContent := s.extractCacheableContent(parsed)
	if cacheableContent != "" {
		hash := s.hashContent(cacheableContent)
		slog.Info("sticky.hash_source",
			"source", "cacheable_content",
			"hash", hash,
		)
		return hash
	}

	// 3. 最后 fallback: 使用 session上下文 + system + 所有消息的完整摘要串
	var combined strings.Builder
	// 混入请求上下文区分因子，避免不同用户相同消息产生相同 hash
	if parsed.SessionContext != nil {
		_, _ = combined.WriteString(parsed.SessionContext.ClientIP)
		_, _ = combined.WriteString(":")
		_, _ = combined.WriteString(NormalizeSessionUserAgent(parsed.SessionContext.UserAgent))
		_, _ = combined.WriteString(":")
		_, _ = combined.WriteString(strconv.FormatInt(parsed.SessionContext.APIKeyID, 10))
		_, _ = combined.WriteString("|")
	}
	if systemText := extractTextFromSystemRaw(parsed.SystemRaw()); systemText != "" {
		_, _ = combined.WriteString(systemText)
	}
	contentStart := combined.Len()
	appendMessageTextsFromRaw(&combined, parsed.MessagesRaw())
	if combined.Len() == contentStart {
		appendResponsesSessionAnchorFromRaw(&combined, parsed.InputRaw())
	}
	if combined.Len() > 0 {
		hash := s.hashContent(combined.String())
		slog.Info("sticky.hash_source",
			"source", "message_content_fallback",
			"hash", hash,
			"content_len", combined.Len(),
		)
		return hash
	}

	return ""
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

func (s *GatewayService) extractCacheableContent(parsed *ParsedRequest) string {
	if parsed == nil {
		return ""
	}

	systemText := extractCacheableTextFromSystemRaw(parsed.SystemRaw())
	if messageText := extractCacheableTextFromMessagesRaw(parsed.MessagesRaw()); messageText != "" {
		return messageText
	}
	return systemText
}

func parseRawJSONView(raw []byte) gjson.Result {
	if len(raw) == 0 {
		return gjson.Result{}
	}
	// 这里只做同步只读解析，避免 gjson.ParseBytes 为大 messages/contents 复制整段 raw。
	return gjson.Parse(*(*string)(unsafe.Pointer(&raw)))
}

func extractTextFromSystemRaw(raw []byte) string {
	system := parseRawJSONView(raw)
	switch system.Type {
	case gjson.String:
		return system.String()
	case gjson.JSON:
		if !system.IsArray() {
			return ""
		}
		var builder strings.Builder
		system.ForEach(func(_, part gjson.Result) bool {
			if text := part.Get("text").String(); text != "" {
				_, _ = builder.WriteString(text)
			}
			return true
		})
		return builder.String()
	}
	return ""
}

func extractTextFromContentRaw(content gjson.Result) string {
	switch content.Type {
	case gjson.String:
		return content.String()
	case gjson.JSON:
		if !content.IsArray() {
			return ""
		}
		var builder strings.Builder
		content.ForEach(func(_, part gjson.Result) bool {
			if part.Get("type").String() == "text" {
				if text := part.Get("text").String(); text != "" {
					_, _ = builder.WriteString(text)
				}
			}
			return true
		})
		return builder.String()
	}
	return ""
}

func appendMessageTextsFromRaw(builder *strings.Builder, raw []byte) {
	if builder == nil || len(raw) == 0 {
		return
	}
	messages := parseRawJSONView(raw)
	if !messages.IsArray() {
		return
	}
	messages.ForEach(func(_, msg gjson.Result) bool {
		if content := msg.Get("content"); content.Exists() {
			_, _ = builder.WriteString(extractTextFromContentRaw(content))
			return true
		}
		parts := msg.Get("parts")
		if parts.IsArray() {
			parts.ForEach(func(_, part gjson.Result) bool {
				if text := part.Get("text").String(); text != "" {
					_, _ = builder.WriteString(text)
				}
				return true
			})
		}
		return true
	})
}

func appendResponsesSessionAnchorFromRaw(builder *strings.Builder, raw []byte) {
	if builder == nil || len(raw) == 0 {
		return
	}
	input := parseRawJSONView(raw)
	if input.Type == gjson.String {
		_, _ = builder.WriteString(input.String())
		return
	}
	if !input.IsArray() {
		return
	}

	input.ForEach(func(_, item gjson.Result) bool {
		if item.Type == gjson.String {
			_, _ = builder.WriteString(item.String())
			return false
		}

		switch item.Get("role").String() {
		case "system", "developer":
			appendResponsesContentText(builder, item.Get("content"))
		case "user":
			appendResponsesContentText(builder, item.Get("content"))
			return false
		default:
			if item.Get("type").String() == "input_text" {
				if text := item.Get("text").String(); text != "" {
					_, _ = builder.WriteString(text)
				}
				return false
			}
		}
		return true
	})
}

func appendResponsesContentText(builder *strings.Builder, content gjson.Result) {
	if builder == nil || !content.Exists() {
		return
	}
	if content.Type == gjson.String {
		_, _ = builder.WriteString(content.String())
		return
	}
	if !content.IsArray() {
		return
	}
	content.ForEach(func(_, part gjson.Result) bool {
		switch part.Get("type").String() {
		case "input_text", "text":
			if text := part.Get("text").String(); text != "" {
				_, _ = builder.WriteString(text)
			}
		}
		return true
	})
}

func extractCacheableTextFromSystemRaw(raw []byte) string {
	system := parseRawJSONView(raw)
	if !system.IsArray() {
		return ""
	}
	var builder strings.Builder
	system.ForEach(func(_, part gjson.Result) bool {
		if part.Get("cache_control.type").String() == "ephemeral" {
			if text := part.Get("text").String(); text != "" {
				_, _ = builder.WriteString(text)
			}
		}
		return true
	})
	return builder.String()
}

func extractCacheableTextFromMessagesRaw(raw []byte) string {
	messages := parseRawJSONView(raw)
	if !messages.IsArray() {
		return ""
	}
	var text string
	messages.ForEach(func(_, msg gjson.Result) bool {
		content := msg.Get("content")
		if !content.IsArray() {
			return true
		}
		found := false
		content.ForEach(func(_, part gjson.Result) bool {
			if part.Get("cache_control.type").String() == "ephemeral" {
				found = true
				return false
			}
			return true
		})
		if found {
			text = extractTextFromContentRaw(content)
			return false
		}
		return true
	})
	return text
}

func (s *GatewayService) hashContent(content string) string {
	h := xxhash.Sum64String(content)
	return strconv.FormatUint(h, 36)
}

// GetAccessToken 获取账号凭证
// @project-doc docs/interfaces/anthropic_upstream.md#anthropic_account_and_transport
func (s *GatewayService) GetAccessToken(ctx context.Context, account *Account) (string, string, error) {
	switch account.Type {
	case AccountTypeOAuth, AccountTypeSetupToken:
		// Both oauth and setup-token use OAuth token flow
		return s.getOAuthToken(ctx, account)
	case AccountTypeAPIKey:
		apiKey := account.GetCredential("api_key")
		if apiKey == "" {
			return "", "", errors.New("api_key not found in credentials")
		}
		return apiKey, "apikey", nil
	case AccountTypeBedrock:
		return "", "bedrock", nil // Bedrock 使用 SigV4 签名或 API Key，由 forwardBedrock 处理
	case AccountTypeServiceAccount:
		if account.Platform != PlatformAnthropic {
			return "", "", fmt.Errorf("unsupported service account platform: %s", account.Platform)
		}
		if s.claudeTokenProvider == nil {
			return "", "", errors.New("claude token provider not configured")
		}
		accessToken, err := s.claudeTokenProvider.GetAccessToken(ctx, account)
		if err != nil {
			return "", "", err
		}
		return accessToken, "service_account", nil
	default:
		return "", "", fmt.Errorf("unsupported account type: %s", account.Type)
	}
}

func (s *GatewayService) getOAuthToken(ctx context.Context, account *Account) (string, string, error) {
	// 对于 Anthropic OAuth 账号，使用 ClaudeTokenProvider 获取缓存的 token
	if account.Platform == PlatformAnthropic && account.Type == AccountTypeOAuth && s.claudeTokenProvider != nil {
		accessToken, err := s.claudeTokenProvider.GetAccessToken(ctx, account)
		if err != nil {
			return "", "", err
		}
		return accessToken, "oauth", nil
	}

	// Grok OAuth 优先使用凭据中的 access_token，后台刷新器负责保持其有效。
	if account.Platform == PlatformGrok && account.Type == AccountTypeOAuth {
		accessToken := account.GetGrokAccessToken()
		if accessToken == "" {
			return "", "", errors.New("grok access_token not found in credentials")
		}
		return accessToken, "oauth", nil
	}

	// 其他情况（Gemini 有自己的 TokenProvider，setup-token 类型等）直接从账号读取
	accessToken := account.GetCredential("access_token")
	if accessToken == "" {
		return "", "", errors.New("access_token not found in credentials")
	}
	// Token刷新由后台 TokenRefreshService 处理，此处只返回当前token
	return accessToken, "oauth", nil
}

// DoGrokNativeResponsesJSON 发送非流式 Grok Responses 请求，供原生网页搜索复用。
func (s *GatewayService) DoGrokNativeResponsesJSON(ctx context.Context, account *Account, body []byte) ([]byte, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, errors.New("http upstream not configured")
	}
	if account == nil || !account.IsGrok() {
		return nil, errors.New("grok account required")
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode: http.StatusUnauthorized,
			Reason:     GatewayFailureReason("grok_search_token"),
		}
	}
	targetURL, err := buildGrokResponsesURL(account, nil, s.settingService)
	if err != nil {
		return nil, err
	}
	if json.Valid(body) && strings.TrimSpace(gjson.GetBytes(body, "model").String()) == "" {
		if patched, patchErr := sjson.SetBytes(body, "model", xai.DefaultTextModel); patchErr == nil {
			body = patched
		}
	}
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build grok responses request: %w", err)
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+token)
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept", "application/json")
	upstreamReq.Header.Set("User-Agent", defaultGrokUpstreamUserAgent())
	applyGrokCLIHeaders(upstreamReq.Header)
	account.ApplyHeaderOverrides(upstreamReq.Header)
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode: http.StatusBadGateway,
			Reason:     GatewayFailureReason("grok_search_transport"),
		}
	}
	defer func() { _ = resp.Body.Close() }()
	respBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return nil, &UpstreamFailoverError{
			StatusCode: http.StatusBadGateway,
			Reason:     GatewayFailureReason("grok_search_read"),
		}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusPaymentRequired ||
			resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode >= http.StatusInternalServerError {
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBytes}
		}
		message := string(respBytes)
		if len(message) > 200 {
			message = message[:200]
		}
		return nil, fmt.Errorf("grok upstream %d: %s", resp.StatusCode, message)
	}
	return respBytes, nil
}

// GetAvailableModels 返回分组下可见的模型列表。
// 它会聚合每个账号显式配置的“可请求模型”（model_mapping 的 key 或独立 model_whitelist）。
func (s *GatewayService) GetAvailableModels(ctx context.Context, groupID *int64, platform string) []string {
	cacheKey := modelsListCacheKey(groupID, platform)
	if s.modelsListCache != nil {
		if cached, found := s.modelsListCache.Get(cacheKey); found {
			if models, ok := cached.([]string); ok {
				modelsListCacheHitTotal.Add(1)
				return cloneStringSlice(models)
			}
		}
	}
	modelsListCacheMissTotal.Add(1)

	var accounts []Account
	var err error

	if groupID != nil {
		accounts, err = s.accountRepo.ListSchedulableByGroupID(ctx, *groupID)
	} else {
		accounts, err = s.accountRepo.ListSchedulable(ctx)
	}

	if err != nil || len(accounts) == 0 {
		return nil
	}

	models := configuredRequestModelsFromAccounts(accounts, platform)
	// 没有账号显式模型范围时返回 nil，由调用方使用平台默认模型。
	if len(models) == 0 {
		if s.modelsListCache != nil {
			s.modelsListCache.Set(cacheKey, []string(nil), s.modelsListCacheTTL)
			modelsListCacheStoreTotal.Add(1)
		}
		return nil
	}

	if s.modelsListCache != nil {
		s.modelsListCache.Set(cacheKey, cloneStringSlice(models), s.modelsListCacheTTL)
		modelsListCacheStoreTotal.Add(1)
	}
	return cloneStringSlice(models)
}

func (s *GatewayService) InvalidateAvailableModelsCache(groupID *int64, platform string) {
	if s == nil || s.modelsListCache == nil {
		return
	}

	normalizedPlatform := strings.TrimSpace(platform)
	// 完整匹配时精准失效；否则按维度批量失效。
	if groupID != nil && normalizedPlatform != "" {
		s.modelsListCache.Delete(modelsListCacheKey(groupID, normalizedPlatform))
		return
	}

	targetGroup := derefGroupID(groupID)
	for key := range s.modelsListCache.Items() {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			continue
		}
		groupPart, parseErr := strconv.ParseInt(parts[0], 10, 64)
		if parseErr != nil {
			continue
		}
		if groupID != nil && groupPart != targetGroup {
			continue
		}
		if normalizedPlatform != "" && parts[1] != normalizedPlatform {
			continue
		}
		s.modelsListCache.Delete(key)
	}
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
	for _, k := range sortHeadersByWireOrder(headers) {
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

// captureDataSharingBestEffort 在使用记录成功后异步旁路采集数据共享 session，失败不影响网关主链路。
func (s *GatewayService) captureDataSharingBestEffort(input *recordUsageCoreInput, result *ForwardResult, requestedModel string, actualCost float64) {
	if s == nil || s.dataSharingService == nil || input == nil || result == nil || input.APIKey == nil || input.APIKey.Group == nil || !input.APIKey.Group.DataSharingEnabled {
		return
	}
	s.dataSharingService.CaptureClaudeRequestAsync(DataShareCaptureInput{
		APIKey:            input.APIKey,
		User:              input.User,
		Account:           input.Account,
		Provider:          PlatformFromAPIKey(input.APIKey),
		Model:             requestedModel,
		UpstreamModel:     result.UpstreamModel,
		SessionID:         input.SessionID,
		RequestID:         result.RequestID,
		RequestBody:       cloneDataSharingRequestBody(input.RequestBody),
		ResponseBody:      cloneDataSharingRequestBody(result.ResponseBody),
		SystemPrompt:      extractSystemPromptFromRequest(input.RequestBody),
		InputTokens:       result.Usage.InputTokens,
		OutputTokens:      result.Usage.OutputTokens,
		CacheReadTokens:   result.Usage.CacheReadInputTokens,
		CacheCreateTokens: result.Usage.CacheCreationInputTokens,
		ActualCost:        &actualCost,
		UserAgent:         input.UserAgent,
		IPAddress:         input.IPAddress,
		InboundEndpoint:   input.InboundEndpoint,
		UpstreamEndpoint:  input.UpstreamEndpoint,
	})
}

func (s *GatewayService) qoderCanUseDefaultImagePricing(model string) bool {
	model = strings.TrimSpace(model)
	if model == "" || qoderAliasRequiresManualPricingAny(model) {
		return false
	}
	if qoderKnownDefaultImagePricingModel(model) {
		return true
	}
	return s != nil && s.billingService != nil && hasExplicitImageGenerationPricing(s.billingService.getRawModelPricing(model))
}

func (s *GatewayService) resolveChannelPricingForUsage(
	ctx context.Context,
	billingModel string,
	requestedModel string,
	billingModelSource string,
	channelMappedModel string,
	baseModelHint string,
	apiKey *APIKey,
	account *Account,
) (*ResolvedPricing, string) {
	if isQoderBillingContext(account, apiKey) {
		return s.resolveQoderChannelPricingForUsage(ctx, billingModel, apiKey)
	}
	if resolved := s.resolveChannelPricingWithBaseHint(ctx, billingModel, baseModelHint, apiKey); resolved != nil {
		return resolved, billingModel
	}
	return nil, billingModel
}

func (s *GatewayService) resolveChannelPricingWithBaseHint(ctx context.Context, billingModel string, baseModelHint string, apiKey *APIKey) *ResolvedPricing {
	if s.resolver == nil || apiKey == nil || apiKey.Group == nil {
		return nil
	}
	gid := apiKey.Group.ID
	resolved := s.resolver.Resolve(ctx, PricingInput{
		Model:         billingModel,
		GroupID:       &gid,
		BaseModelHint: baseModelHint,
		Group:         apiKey.Group,
	})
	if resolved.Source == PricingSourceGroup || resolved.Source == PricingSourceChannel {
		return resolved
	}
	return nil
}

func (s *GatewayService) resolveQoderChannelPricingForUsage(
	ctx context.Context,
	billingModel string,
	apiKey *APIKey,
) (*ResolvedPricing, string) {
	// Qoder 必须严格按渠道选定的计费模型匹配，不能跨 R/C/U 寻找其他价格行。
	billingModel = strings.TrimSpace(billingModel)
	resolved := s.resolveChannelPricingWithBaseHint(ctx, billingModel, "", apiKey)
	if resolved != nil && (resolved.Source == PricingSourceGroup || resolved.HasEffectiveChannelPricing()) {
		return resolved, billingModel
	}
	return nil, billingModel
}

// ObserveData 解析单条 SSE data 行，并交给 ObserveEvent 更新快照。
func (a *anthropicStreamResponseAccumulator) ObserveData(eventName string, data string) []byte {
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return nil
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}
	return a.ObserveEvent(eventName, event)
}

// ObserveEvent 聚合 Anthropic SSE 事件，尽量还原最终 message 响应体。
func (a *anthropicStreamResponseAccumulator) ObserveEvent(eventName string, event map[string]any) []byte {
	if a == nil || event == nil {
		return nil
	}
	eventType, _ := event["type"].(string)
	if eventType == "" {
		eventType = eventName
	}
	switch eventType {
	case "message_start":
		if msg, ok := event["message"].(map[string]any); ok && len(msg) > 0 {
			a.message = cloneDataShareMap(msg)
			if _, ok := a.message["type"]; !ok {
				a.message["type"] = "message"
			}
			if _, ok := a.message["role"]; !ok {
				a.message["role"] = "assistant"
			}
			a.content = nil
			if blocks := mapsFromAny(a.message["content"]); len(blocks) > 0 {
				a.content = append(a.content, blocks...)
			}
			return a.ResponseBody()
		}
	case "content_block_start":
		index := intFromAny(event["index"])
		block, ok := mapFromAny(event["content_block"])
		if !ok {
			return nil
		}
		a.ensureContentIndex(index)
		a.content[index] = cloneDataShareMap(block)
		return a.ResponseBody()
	case "content_block_delta":
		index := intFromAny(event["index"])
		delta, ok := mapFromAny(event["delta"])
		if !ok {
			return nil
		}
		a.applyContentDelta(index, delta)
		return a.ResponseBody()
	case "message_delta":
		if a.message == nil {
			a.message = map[string]any{"role": "assistant", "type": "message"}
		}
		if delta, ok := mapFromAny(event["delta"]); ok {
			for key, value := range delta {
				a.message[key] = value
			}
		}
		if usage, ok := mapFromAny(event["usage"]); ok {
			existingUsage, _ := mapFromAny(a.message["usage"])
			mergedUsage := cloneDataShareMap(existingUsage)
			for key, value := range usage {
				mergedUsage[key] = value
			}
			a.message["usage"] = mergedUsage
		}
		return a.ResponseBody()
	case "message_stop":
		if body := a.ResponseBody(); len(body) > 0 {
			return body
		}
		body, _ := json.Marshal(event)
		return body
	}
	return nil
}

// ResponseBody 输出当前已聚合出的 Anthropic message 快照。
func (a *anthropicStreamResponseAccumulator) ResponseBody() []byte {
	if a == nil || a.message == nil {
		return nil
	}
	msg := cloneDataShareMap(a.message)
	if len(a.content) > 0 {
		content := make([]map[string]any, 0, len(a.content))
		for _, block := range a.content {
			content = append(content, finalizeAnthropicStreamContentBlock(block))
		}
		msg["content"] = content
	}
	if _, ok := msg["role"]; !ok {
		msg["role"] = "assistant"
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil
	}
	return body
}

// applyContentDelta 合并 text_delta 和 input_json_delta，构造最终 content block。
func (a *anthropicStreamResponseAccumulator) applyContentDelta(index int, delta map[string]any) {
	a.ensureContentIndex(index)
	block := a.content[index]
	switch strings.TrimSpace(stringFromAny(delta["type"])) {
	case "text_delta":
		block["text"] = stringFromAny(block["text"]) + stringFromAny(delta["text"])
	case "input_json_delta":
		block["partial_json"] = stringFromAny(block["partial_json"]) + stringFromAny(delta["partial_json"])
	default:
		for key, value := range delta {
			if key == "type" {
				continue
			}
			block[key] = value
		}
	}
}

// ensureContentIndex 扩展 content 切片，保证指定 index 可写。
func (a *anthropicStreamResponseAccumulator) ensureContentIndex(index int) {
	if index < 0 {
		index = 0
	}
	for len(a.content) <= index {
		a.content = append(a.content, map[string]any{})
	}
	if a.content[index] == nil {
		a.content[index] = map[string]any{}
	}
}

func (p *usageBillingParams) shouldDeductAPIKeyQuota() bool {
	return p.Cost.ActualCost > 0 && p.APIKey.Quota > 0 && p.APIKeyService != nil
}

func (p *usageBillingParams) shouldUpdateAccountQuota(accountQuotaCost float64) bool {
	return accountQuotaCost > 0 && p.Account.IsAPIKeyOrBedrock() && p.Account.HasAnyQuotaLimit()
}

func (p *usageBillingParams) shouldUpdateRateLimits() bool {
	return p.Cost.ActualCost > 0 && p.APIKey.HasRateLimits() && p.APIKeyService != nil
}

func applyUsageBillingResultToUsageLog(usageLog *UsageLog, result *UsageBillingApplyResult) {
	if usageLog == nil || result == nil {
		return
	}

	usageLog.SubscriptionAmountUSD = result.SubscriptionAmountUSD
	usageLog.BalanceAmountUSD = result.BalanceAmountUSD
	usageLog.BillingAllocations = cloneBillingAllocations(result.BillingAllocations)
	usageLog.SubscriptionID = firstAllocatedSubscriptionID(result.BillingAllocations)
	if billable := usageBillingResultBillableAmount(result); billable >= 0 && result.EffectiveRateMultiplier != nil {
		usageLog.ActualCost = billable
		usageLog.RateMultiplier = *result.EffectiveRateMultiplier
	}
	switch {
	case result.SubscriptionAmountUSD > 0:
		usageLog.BillingType = BillingTypeSubscription
	default:
		usageLog.BillingType = BillingTypeBalance
	}
}

func cloneBillingAllocations(allocations []domain.BillingAllocation) []domain.BillingAllocation {
	if len(allocations) == 0 {
		return nil
	}
	cloned := make([]domain.BillingAllocation, 0, len(allocations))
	for i := range allocations {
		allocation := allocations[i]
		if allocation.SubscriptionID != nil {
			subscriptionID := *allocation.SubscriptionID
			allocation.SubscriptionID = &subscriptionID
		}
		if allocation.PlanID != nil {
			planID := *allocation.PlanID
			allocation.PlanID = &planID
		}
		cloned = append(cloned, allocation)
	}
	return cloned
}

// finalizeAnthropicStreamContentBlock 把流式工具参数的 partial_json 转成最终 input。
func finalizeAnthropicStreamContentBlock(block map[string]any) map[string]any {
	out := cloneDataShareMap(block)
	if strings.TrimSpace(stringFromAny(out["type"])) == "tool_use" {
		if raw := strings.TrimSpace(stringFromAny(out["partial_json"])); raw != "" {
			var parsed any
			if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
				out["input"] = parsed
			} else {
				out["input"] = raw
			}
			delete(out, "partial_json")
		}
	}
	return out
}

// @project-doc docs/domains/platform_quotas.md#platform_quota_settlement_and_flush
func finalizeUsageBilling(p *usageBillingParams, deps *billingDeps, result *UsageBillingApplyResult) {
	if p == nil || p.Cost == nil || deps == nil {
		return
	}

	if result != nil && result.BalanceAmountUSD > 0 && p.User != nil {
		syncBalanceCacheAfterDeduction(context.Background(), p, deps, result)
	}

	rateLimitCost := p.Cost.ActualCost
	if result != nil {
		rateLimitCost = usageBillingResultBillableAmount(result)
	}
	if rateLimitCost > 0 && p.APIKey != nil && p.APIKey.HasRateLimits() {
		deps.billingCacheService.QueueUpdateAPIKeyRateLimitUsage(p.APIKey.ID, rateLimitCost)
	}

	deps.deferredService.ScheduleLastUsedUpdate(p.Account.ID)

	// Platform quota 累加：按实际余额扣费金额生效；纯订阅扣费豁免；仅对有 limit 的用户写。
	// Redis 同步写 + DB 异步持久化（flag=false 降级）或 flusher 异步刷（flag=true）：
	//   - HasUserPlatformQuotaLimit 守卫：无 limit 的用户跳过，避免无效写入和 Redis 容量浪费
	//   - Redis 同步：确保下次预检查立即看到最新 usage，把 TOCTOU 超支窗口
	//     限制在并发 in-flight 请求数量内（旧实现的异步入队会让超支无限累积直到 worker 处理）
	//   - DB 异步（flusher_enabled=false）：在独立 goroutine 中走 detached context，失败用 ALERT 日志触发 oncall 对账
	//   - flusher_enabled=true：不直写 DB，由 flusher 异步批量刷（markDirty 已在 IncrementUserPlatformQuotaUsage 内部完成）
	balanceCost := p.Cost.ActualCost
	if result != nil {
		balanceCost = result.BalanceAmountUSD
	}
	if IsAllowedQuotaPlatform(p.Platform) && balanceCost > 0 && p.User != nil && deps.userPlatformQuotaRepo != nil && deps.billingCacheService != nil {
		limitCtx, limitCancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
		hasLimit := deps.billingCacheService.HasUserPlatformQuotaLimit(limitCtx, p.User.ID, p.Platform)
		limitCancel()
		if hasLimit {
			deps.billingCacheService.IncrementUserPlatformQuotaUsage(p.User.ID, p.Platform, balanceCost)
			if deps.cfg == nil || !deps.cfg.Database.UserPlatformQuotaFlusherEnabled {
				dbCtx, dbCancel := detachUpstreamContext(context.Background())
				userID, platform, cost := p.User.ID, p.Platform, balanceCost
				go func() {
					defer func() {
						if r := recover(); r != nil {
							logger.LegacyPrintf("service.gateway", "ALERT: panic in user platform quota incr goroutine user=%d platform=%s: %v", userID, platform, r)
						}
					}()
					defer dbCancel()
					if err := deps.userPlatformQuotaRepo.IncrementUsageWithReset(dbCtx, userID, platform, cost, time.Now().UTC()); err != nil {
						// 失败计数器：暴露给 GatewayUserPlatformQuotaIncrStats()，由 ops 面板做斜率告警。
						userPlatformQuotaDBIncrErrorTotal.Add(1)
						// ALERT 级别：DB 持久化失败意味着 Redis cache 失效后该笔 cost 永久丢失，
						// 用户配额视图与实际消费会偏差，oncall 需要据此对账或人工补录。
						logger.LegacyPrintf("service.gateway", "ALERT: incr user platform quota DB failed user=%d platform=%s cost=%f: %v", userID, platform, cost, err)
					}
				}()
			}
		}
	}

	// 通知检查异步执行，所需参数已全部捕获，不依赖请求 context 或上游连接。
	go notifyBalanceLow(p, deps, result)
	go notifyAccountQuota(p, deps, result)
}

func usageBillingResultBillableAmount(result *UsageBillingApplyResult) float64 {
	if result == nil {
		return -1
	}
	return result.SubscriptionAmountUSD + result.BalanceAmountUSD
}

func firstAllocatedSubscriptionID(allocations []domain.BillingAllocation) *int64 {
	for i := range allocations {
		if allocations[i].Type != domain.BillingAllocationTypeSubscription || allocations[i].SubscriptionID == nil {
			continue
		}
		subscriptionID := *allocations[i].SubscriptionID
		return &subscriptionID
	}
	return nil
}

func isQoderBillingContext(account *Account, apiKey *APIKey) bool {
	if account != nil && account.Platform == PlatformQoder {
		return true
	}
	return apiKey != nil && apiKey.Group != nil && apiKey.Group.Platform == PlatformQoder
}

// qoderAliasRequiresManualPricingInContext 在 Qoder 账号上下文中判断 alias 是否必须使用手工定价。
//
//nolint:unused // 保留语义化判断入口；当前生产路径已在各调用点直接判断 Qoder 上下文。
func qoderAliasRequiresManualPricingInContext(account *Account, apiKey *APIKey, models ...string) bool {
	if !isQoderBillingContext(account, apiKey) {
		return false
	}
	return qoderAliasRequiresManualPricingAny(models...)
}

func qoderKnownDefaultImagePricingModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gpt-image-") ||
		strings.HasPrefix(model, "dall-e") ||
		strings.HasPrefix(model, "imagen-")
}

func zeroCostBreakdown(mode BillingMode) *CostBreakdown {
	modeString := string(mode)
	if modeString == "" {
		modeString = string(BillingModeToken)
	}
	return &CostBreakdown{BillingMode: modeString}
}

type anthropicStreamResponseAccumulator struct {
	message map[string]any
	content []map[string]any
}

// usageBillingParams 统一扣费所需的参数
type usageBillingParams struct {
	Cost                            *CostBreakdown
	User                            *User
	APIKey                          *APIKey
	Account                         *Account
	Subscription                    *UserSubscription
	RequestPayloadHash              string
	AccountRateMultiplier           float64
	SubscriptionRateMultiplier      float64
	SubscriptionRateMultiplierScale float64
	BalanceRateMultiplier           float64
	APIKeyService                   APIKeyQuotaUpdater
	Platform                        string // 来自 APIKey 关联 Group 的平台标识
}

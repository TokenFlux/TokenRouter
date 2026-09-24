package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"golang.org/x/net/http/httpguts"
)

const openCodeSessionHeader = "X-OpenCode-Session"

// ApplyOpenCodeSessionHeader forwards the caller-owned conversation identifier
// only to OpenCode's official API origin. The caller applies this after account
// header overrides so a per-conversation value cannot be replaced by a fixed
// account-wide override.
func ApplyOpenCodeSessionHeader(c *gin.Context, account *gatewayprovider.ExecutionAccount, targetURL string, headers http.Header) {
	if c == nil || c.Request == nil || account == nil || account.Record.Type != capability.AccountTypeAPIKey || headers == nil {
		return
	}

	parsed, err := url.Parse(targetURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "opencode.ai") {
		return
	}

	sessionID := strings.TrimSpace(c.GetHeader(openCodeSessionHeader))
	if sessionID == "" {
		return
	}
	for key := range headers {
		if strings.EqualFold(key, openCodeSessionHeader) {
			delete(headers, key)
		}
	}
	headers.Set(openCodeSessionHeader, sessionID)
}
func ResolveOpenAIUpstreamOriginator(c *gin.Context, isOfficialClient bool, routerMatch ...egress.TLSFingerprintRouterMatchResult) string {
	return ResolveOpenAIUpstreamOriginatorForClient(func() string {
		if c == nil {
			return ""
		}
		return c.GetHeader("originator")
	}, isOfficialClient, routerMatch...)
}

// originator 的平台规则由 upstream 唯一执行，旧入口只投影路由结果。
func ResolveOpenAIUpstreamOriginatorForClient(read func() string, official bool, matches ...egress.TLSFingerprintRouterMatchResult) string {
	var match egress.TLSFingerprintRouterMatchResult
	if len(matches) > 0 {
		match = matches[0]
	}
	return openai.ResolveUpstreamOriginator(read, official, match.Matched, match.UpstreamOriginator)
}

const OpenAICodexRoutingHintHeader = "x-codex-routing-hint"

// SetOpenAICodexRoutingHint 为 OpenAI OAuth 请求生成 Codex 后端路由提示。
// model 必须是最终上游模型名，serviceTier 必须已应用本地策略改写与过滤。
func SetOpenAICodexRoutingHint(headers http.Header, account *gatewayprovider.ExecutionAccount, model string, serviceTier string) {
	if headers == nil {
		return
	}

	// 路由提示由网关独占控制。生成前删除所有大小写变体，避免 API Key、
	// Provider 凭证路径透传调用方或账号头覆盖注入的提示；Header.Del 只会
	// 删除规范化键，而入站映射可能保留原始小写键。
	DeleteOpenAIHeaderEqualFold(headers, OpenAICodexRoutingHintHeader)
	if account == nil || !account.View().IsOpenAIOAuthLike() {
		return
	}

	model = strings.TrimSpace(model)
	if model == "" || strings.ContainsAny(model, ";=") {
		return
	}

	// Codex 将 default 视为标准路由哨兵而非发往后端的服务层级；fast 沿用
	// 网关现有规范化规则转为 priority，flex 和 ultrafast 保持不变。
	canonicalTier := protocolopenai.ServiceTierValue(serviceTier)
	// 当前回移不含 Codex 模型目录快照，无法校验任意层级 ID，因此只发送
	// Codex 实际选择的有效层级；default、空值和其他兼容 API 值仅保留模型。
	switch canonicalTier {
	case tierpolicy.OpenAIFastTierPriority, tierpolicy.OpenAIFastTierFlex, tierpolicy.OpenAIFastTierUltrafast:
	default:
		canonicalTier = ""
	}

	hint := "model=" + model
	if canonicalTier != "" {
		hint += ";tier=" + canonicalTier
	}
	if !httpguts.ValidHeaderFieldValue(hint) {
		return
	}
	headers.Set(OpenAICodexRoutingHintHeader, hint)
}
func DeleteOpenAIHeaderEqualFold(headers http.Header, name string) {
	if headers == nil {
		return
	}
	name = strings.TrimSpace(name)
	for key := range headers {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			delete(headers, key)
		}
	}
}
func SetOpenAICodexRoutingHintFromBody(headers http.Header, account *gatewayprovider.ExecutionAccount, body []byte) {
	fields := gjson.GetManyBytes(body, "model", "service_tier")
	SetOpenAICodexRoutingHint(headers, account, fields[0].String(), fields[1].String())
}

// LogOpenAIRoutingDiagnostics 仅记录网关推导出的路由状态；该逻辑位于携带认证
// 信息的链路中，因此明确不记录任何请求头值、令牌或凭证。
func LogOpenAIRoutingDiagnostics(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	transport string,
	model string,
	serviceTier string,
	hintGenerated bool,
	wsAffinityDecision string,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.Record.ID
	}

	logging.FromContext(ctx).Debug("openai routing decision",
		zap.String("component", "service.openai_routing"),
		zap.String("transport", strings.TrimSpace(transport)),
		zap.Int64("account_id", accountID),
		zap.String("final_model", strings.TrimSpace(model)),
		zap.String("final_service_tier", protocolopenai.ServiceTierValue(serviceTier)),
		zap.Bool("routing_hint_generated", hintGenerated),
		zap.String("ws_affinity_decision", strings.TrimSpace(wsAffinityDecision)),
	)
}
func LogOpenAIRoutingDiagnosticsFromBody(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	transport string,
	headers http.Header,
	body []byte,
	wsAffinityDecision string,
) {
	fields := gjson.GetManyBytes(body, "model", "service_tier")
	LogOpenAIRoutingDiagnostics(
		ctx,
		account,
		transport,
		fields[0].String(),
		fields[1].String(),
		strings.TrimSpace(headers.Get(OpenAICodexRoutingHintHeader)) != "",
		wsAffinityDecision,
	)
}

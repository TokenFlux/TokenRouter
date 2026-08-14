package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"golang.org/x/net/http/httpguts"
)

const openAICodexRoutingHintHeader = "x-codex-routing-hint"

// setOpenAICodexRoutingHint 为 OpenAI OAuth 请求生成 Codex 后端路由提示。
// model 必须是最终上游模型名，serviceTier 必须已应用本地策略改写与过滤。
func setOpenAICodexRoutingHint(headers http.Header, account *Account, model string, serviceTier string) {
	if headers == nil {
		return
	}

	// 路由提示由网关独占控制。生成前删除所有大小写变体，避免 API Key、
	// Provider 凭证路径透传调用方或账号头覆盖注入的提示；Header.Del 只会
	// 删除规范化键，而入站映射可能保留原始小写键。
	deleteOpenAIHeaderEqualFold(headers, openAICodexRoutingHintHeader)
	if account == nil || !account.IsOpenAIOAuth() {
		return
	}

	model = strings.TrimSpace(model)
	if model == "" || strings.ContainsAny(model, ";=") {
		return
	}

	// Codex 将 default 视为标准路由哨兵而非发往后端的服务层级；fast 沿用
	// 网关现有规范化规则转为 priority，flex 保持不变。
	canonicalTier := normalizedOpenAIServiceTierValue(serviceTier)
	// 当前回移不含 Codex 模型目录快照，无法校验任意层级 ID，因此只发送
	// Codex 实际选择的两种有效层级；default、空值和其他兼容 API 值仅保留模型。
	switch canonicalTier {
	case OpenAIFastTierPriority, OpenAIFastTierFlex:
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
	headers.Set(openAICodexRoutingHintHeader, hint)
}

func deleteOpenAIHeaderEqualFold(headers http.Header, name string) {
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

func setOpenAICodexRoutingHintFromBody(headers http.Header, account *Account, body []byte) {
	fields := gjson.GetManyBytes(body, "model", "service_tier")
	setOpenAICodexRoutingHint(headers, account, fields[0].String(), fields[1].String())
}

// logOpenAIRoutingDiagnostics 仅记录网关推导出的路由状态；该逻辑位于携带认证
// 信息的链路中，因此明确不记录任何请求头值、令牌或凭证。
func logOpenAIRoutingDiagnostics(
	ctx context.Context,
	account *Account,
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
		accountID = account.ID
	}

	logger.FromContext(ctx).Debug("openai routing decision",
		zap.String("component", "service.openai_routing"),
		zap.String("transport", strings.TrimSpace(transport)),
		zap.Int64("account_id", accountID),
		zap.String("final_model", strings.TrimSpace(model)),
		zap.String("final_service_tier", normalizedOpenAIServiceTierValue(serviceTier)),
		zap.Bool("routing_hint_generated", hintGenerated),
		zap.String("ws_affinity_decision", strings.TrimSpace(wsAffinityDecision)),
	)
}

func logOpenAIRoutingDiagnosticsFromBody(
	ctx context.Context,
	account *Account,
	transport string,
	headers http.Header,
	body []byte,
	wsAffinityDecision string,
) {
	fields := gjson.GetManyBytes(body, "model", "service_tier")
	logOpenAIRoutingDiagnostics(
		ctx,
		account,
		transport,
		fields[0].String(),
		fields[1].String(),
		strings.TrimSpace(headers.Get(openAICodexRoutingHintHeader)) != "",
		wsAffinityDecision,
	)
}

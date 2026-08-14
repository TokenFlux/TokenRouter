package service

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// GrokUpstreamFailureClass 用于决定临时停调冷却与响应提交前的账号切换。
// 分类优先分析响应体，使代理改写状态码后免费额度耗尽和空输出语义仍能优先命中。
type GrokUpstreamFailureClass string

const (
	GrokFailureNone          GrokUpstreamFailureClass = ""
	GrokFailureFreeUsage     GrokUpstreamFailureClass = "subscription:free-usage-exhausted"
	GrokFailureBilling       GrokUpstreamFailureClass = "billing_quota"
	GrokFailureEmptyUpstream GrokUpstreamFailureClass = "empty_upstream"
	GrokFailureModelCapacity GrokUpstreamFailureClass = "model_capacity"
	GrokFailureRateLimit     GrokUpstreamFailureClass = "rate_limit"
	GrokFailureAuth          GrokUpstreamFailureClass = "auth_error"
	GrokFailureServer        GrokUpstreamFailureClass = "server_error"
)

// GrokUpstreamFailureDecision 是纯分类结果，调用方再映射到账号状态更新方法。
type GrokUpstreamFailureDecision struct {
	Class          GrokUpstreamFailureClass
	Model          string
	Cooldown       time.Duration
	ShouldCooldown bool
	// ShouldFailover 表示应在终止响应写入前尝试其它账号；内容策略拒绝由独立路径处理。
	ShouldFailover bool
	// BlockModel 只用于已知模型的空输出；免费额度耗尽默认冷却账号而非单个模型。
	BlockModel   bool
	Reason       string
	TokensActual *int64
	TokensLimit  *int64
}

var (
	reGrokTokenPair = regexp.MustCompile(`(?i)tokens?\s*(?:\(actual\s*/\s*limit\))?\s*[:=]?\s*(\d+)\s*/\s*(\d+)`)
	reGrokModelFor  = regexp.MustCompile(`(?i)(?:for\s+model|model|模型)\s*[:：]?\s*([a-z0-9][a-z0-9._-]{2,80})`)
)

// classifyGrokUpstreamFailure 根据状态码和响应体决定冷却与切换，优先级依次为：
// 免费额度耗尽、账单硬额度、空输出、模型容量、普通限流、5xx；普通校验错误不冷却。
// 内容策略 403 必须由调用方在进入此分类器前排除。
func classifyGrokUpstreamFailure(statusCode int, responseBody []byte, requestedModel string) GrokUpstreamFailureDecision {
	text, code, low := grokUpstreamErrorCorpus(statusCode, responseBody)
	model := extractGrokFailureModel(text, responseBody, requestedModel)
	actual, limit, hasTokens := parseGrokTokenPair(text)
	if !hasTokens {
		actual, limit, hasTokens = parseGrokTokenPair(string(responseBody))
	}

	// 免费用量或滚动额度耗尽。
	if isGrokFreeUsageExhaustedText(low) || isGrokFreeUsageCode(code) || isGrokFreeUsageCode(text) {
		d := GrokUpstreamFailureDecision{
			Class:          GrokFailureFreeUsage,
			Model:          model,
			Cooldown:       grokFreeUsageCooldownDuration(low),
			ShouldCooldown: true,
			ShouldFailover: true,
			BlockModel:     false,
			Reason:         firstNonEmpty(text, code, "free usage exhausted"),
		}
		if hasTokens {
			a, b := actual, limit
			d.TokensActual = &a
			d.TokensLimit = &b
		}
		return d
	}

	// 账单硬额度耗尽保持 30 分钟冷却，与既有 402/消费上限处理一致。
	if isGrokBillingQuotaText(low) || statusCode == http.StatusPaymentRequired {
		reason := firstNonEmpty(text, "billing quota")
		if statusCode == http.StatusPaymentRequired && text == "" {
			reason = "payment required"
		}
		return GrokUpstreamFailureDecision{
			Class:          GrokFailureBilling,
			Model:          model,
			Cooldown:       30 * time.Minute,
			ShouldCooldown: true,
			ShouldFailover: true,
			BlockModel:     model != "",
			Reason:         reason,
		}
	}

	// HTTP 200 空输出或模型空输出，代理可能将其改写为合成 502。
	if isGrokEmptyModelOutputText(low) || isGrokEmptyModelOutputCode(code) {
		return GrokUpstreamFailureDecision{
			Class:          GrokFailureEmptyUpstream,
			Model:          model,
			Cooldown:       4 * time.Minute,
			ShouldCooldown: true,
			ShouldFailover: true,
			BlockModel:     model != "",
			Reason:         firstNonEmpty(text, "empty model output"),
		}
	}

	// 模型容量不足或过载。
	if isGrokModelCapacityText(low) {
		return GrokUpstreamFailureDecision{
			Class:          GrokFailureModelCapacity,
			Model:          model,
			Cooldown:       3 * time.Minute,
			ShouldCooldown: true,
			ShouldFailover: true,
			BlockModel:     false,
			Reason:         firstNonEmpty(text, "model capacity"),
		}
	}

	// 不含免费额度语义的普通限流。
	if statusCode == http.StatusTooManyRequests || isGrokRateLimitText(low) {
		return GrokUpstreamFailureDecision{
			Class:          GrokFailureRateLimit,
			Model:          model,
			Cooldown:       10 * time.Minute,
			ShouldCooldown: true,
			ShouldFailover: true,
			BlockModel:     false,
			Reason:         firstNonEmpty(text, "rate limit"),
		}
	}

	// 普通上游 5xx 短暂冷却；合成的空输出 502 已在前面处理。
	if statusCode >= 500 && statusCode <= 599 {
		return GrokUpstreamFailureDecision{
			Class:          GrokFailureServer,
			Cooldown:       2 * time.Minute,
			ShouldCooldown: true,
			ShouldFailover: true,
			Reason:         firstNonEmpty(text, "server error"),
		}
	}

	return GrokUpstreamFailureDecision{Reason: text}
}

func grokUpstreamErrorCorpus(statusCode int, responseBody []byte) (text, code, low string) {
	_ = statusCode // 分类器已单独持有传输状态，此处语料只分析响应体。
	raw := strings.TrimSpace(string(responseBody))
	// 移除 upstream status 前缀，使免费额度和账单语义可直接识别。
	if _, unwrappedBody, ok := unwrapGrokUpstreamErrorText(raw); ok {
		raw = unwrappedBody
	}
	text = raw
	codeFromJSON, msgFromJSON := parseGrokUpstreamErrorJSON(raw)
	if msgFromJSON != "" {
		if text == "" || len(msgFromJSON) > len(text)/2 || looksLikeGrokQuotaMessage(msgFromJSON) {
			text = msgFromJSON
		}
	}
	// 原始响应体存在结构化字段时优先采用。
	if len(responseBody) > 0 {
		if m := strings.TrimSpace(firstNonEmpty(
			gjson.GetBytes(responseBody, "error.message").String(),
			gjson.GetBytes(responseBody, "message").String(),
			gjson.GetBytes(responseBody, "error").String(),
		)); m != "" && (text == "" || looksLikeGrokQuotaMessage(m)) {
			text = m
		}
		if c := strings.TrimSpace(firstNonEmpty(
			gjson.GetBytes(responseBody, "error.code").String(),
			gjson.GetBytes(responseBody, "code").String(),
		)); c != "" {
			codeFromJSON = c
		}
	}
	code = codeFromJSON
	low = strings.ToLower(strings.TrimSpace(text))
	if code != "" && !strings.Contains(low, strings.ToLower(code)) {
		low = strings.ToLower(code) + " " + low
	}
	return text, code, low
}

func unwrapGrokUpstreamErrorText(errText string) (status int, body string, ok bool) {
	text := strings.TrimSpace(errText)
	if text == "" {
		return 0, "", false
	}
	lower := strings.ToLower(text)
	for _, p := range []string{"upstream status ", "status "} {
		if !strings.HasPrefix(lower, p) {
			continue
		}
		rest := strings.TrimSpace(text[len(p):])
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			status = status*10 + int(rest[i]-'0')
			i++
		}
		if status <= 0 || i == 0 {
			return 0, "", false
		}
		rest = strings.TrimSpace(rest[i:])
		if strings.HasPrefix(rest, ":") {
			rest = strings.TrimSpace(rest[1:])
		}
		return status, rest, true
	}
	return 0, "", false
}

func parseGrokUpstreamErrorJSON(errText string) (code, message string) {
	text := strings.TrimSpace(errText)
	if text == "" || text[0] != '{' {
		return "", ""
	}
	var payload map[string]any
	if json.Unmarshal([]byte(text), &payload) != nil {
		return "", ""
	}
	if v, ok := payload["code"].(string); ok {
		code = v
	}
	if v, ok := payload["message"].(string); ok {
		message = v
	}
	if errObj, ok := payload["error"].(map[string]any); ok {
		if v, ok := errObj["code"].(string); ok && code == "" {
			code = v
		}
		if v, ok := errObj["message"].(string); ok && message == "" {
			message = v
		}
	}
	if errStr, ok := payload["error"].(string); ok && message == "" {
		message = errStr
	}
	return strings.TrimSpace(code), strings.TrimSpace(message)
}

func looksLikeGrokQuotaMessage(s string) bool {
	low := strings.ToLower(s)
	return strings.Contains(low, "quota") ||
		strings.Contains(low, "usage") ||
		strings.Contains(low, "credit") ||
		strings.Contains(low, "额度") ||
		strings.Contains(low, "free")
}

func isGrokFreeUsageCode(code string) bool {
	c := strings.ToLower(strings.TrimSpace(code))
	if c == "" {
		return false
	}
	if strings.Contains(c, "subscription:free-usage-exhausted") ||
		strings.Contains(c, "free-usage-exhausted") ||
		strings.Contains(c, "free_usage_exhausted") ||
		strings.Contains(c, "usage-limit-exceeded") ||
		strings.Contains(c, "usage_limit_exceeded") {
		return true
	}
	return (strings.Contains(c, "free-usage") || strings.Contains(c, "free_usage")) &&
		(strings.Contains(c, "exhaust") || strings.Contains(c, "exceed") || strings.Contains(c, "limit"))
}

func isGrokFreeUsageExhaustedText(low string) bool {
	if low == "" {
		return false
	}
	if strings.Contains(low, "free-usage-exhausted") ||
		strings.Contains(low, "free_usage_exhausted") ||
		strings.Contains(low, "subscription:free-usage") ||
		strings.Contains(low, "usage-limit-exceeded") ||
		strings.Contains(low, "usage_limit_exceeded") ||
		strings.Contains(low, "free-tier-limit") ||
		strings.Contains(low, "free_tier_limit") {
		return true
	}
	if strings.Contains(low, "free usage") ||
		strings.Contains(low, "included free usage") ||
		strings.Contains(low, "used all the included free") ||
		strings.Contains(low, "you've used all the included free") ||
		strings.Contains(low, "you have used all the included free") ||
		strings.Contains(low, "free quota") ||
		strings.Contains(low, "no remaining free") ||
		strings.Contains(low, "out of free") ||
		strings.Contains(low, "usage resets over a rolling") ||
		(strings.Contains(low, "free tier") && (strings.Contains(low, "exhaust") || strings.Contains(low, "limit") || strings.Contains(low, "exceed"))) {
		return true
	}
	for _, p := range []string{
		"额度耗尽", "额度用完", "额度不足", "额度已用尽", "额度已耗尽",
		"免费额度", "免费用量", "用量用完", "用量耗尽", "用量超限", "用量已用尽",
		"配额耗尽", "配额已用尽", "配额不足", "配额超限", "配额用完",
		"没有额度", "没额度", "无额度", "可用额度不足", "模型额度",
		"临时额度", "额度已满", "额度超限", "额度达到上限",
		"模型额度用完", "模型额度耗尽", "账号额度用完", "账号额度耗尽",
		"额度不够", "没额度了", "额度没了", "用完额度", "耗尽额度",
	} {
		if strings.Contains(low, p) {
			return true
		}
	}
	if (strings.Contains(low, "quota") && (strings.Contains(low, "exhaust") || strings.Contains(low, "exceed") || strings.Contains(low, "limit"))) ||
		(strings.Contains(low, "usage") && (strings.Contains(low, "exhaust") || strings.Contains(low, "exceed")) && (strings.Contains(low, "limit") || strings.Contains(low, "free") || strings.Contains(low, "model"))) {
		if strings.Contains(low, "free") || strings.Contains(low, "rolling") ||
			strings.Contains(low, "24-hour") || strings.Contains(low, "24 hour") ||
			strings.Contains(low, "model") || strings.Contains(low, "subscription") ||
			strings.Contains(low, "included") || strings.Contains(low, "tokens") {
			return true
		}
	}
	if a, b, ok := parseGrokTokenPair(low); ok && b > 0 && a >= b {
		if strings.Contains(low, "free") || strings.Contains(low, "subscription") ||
			strings.Contains(low, "included") || strings.Contains(low, "model") ||
			strings.Contains(low, "usage") || strings.Contains(low, "quota") ||
			strings.Contains(low, "rolling") {
			return true
		}
	}
	return false
}

func isGrokBillingQuotaText(low string) bool {
	if low == "" {
		return false
	}
	if strings.Contains(low, "insufficient_quota") {
		return true
	}
	if strings.Contains(low, "billing") && strings.Contains(low, "quota") {
		return true
	}
	if strings.Contains(low, "payment") && (strings.Contains(low, "required") || strings.Contains(low, "fail")) {
		return true
	}
	if strings.Contains(low, "spending limit") || strings.Contains(low, "run out of credits") || strings.Contains(low, "out of credits") {
		return true
	}
	if strings.Contains(low, "余额不足") || strings.Contains(low, "欠费") || strings.Contains(low, "需要付费") {
		return true
	}
	return false
}

func isGrokModelCapacityText(low string) bool {
	return strings.Contains(low, "capacity") ||
		strings.Contains(low, "overloaded") ||
		strings.Contains(low, "server_busy") ||
		strings.Contains(low, "too many concurrent") ||
		strings.Contains(low, "engine_overloaded")
}

func isGrokRateLimitText(low string) bool {
	return strings.Contains(low, "rate limit") ||
		strings.Contains(low, "rate_limit") ||
		strings.Contains(low, "too many requests") ||
		strings.Contains(low, "请求过于频繁") ||
		strings.Contains(low, "速率限制")
}

func isGrokEmptyModelOutputText(low string) bool {
	if low == "" {
		return false
	}
	return strings.Contains(low, "empty model output") ||
		strings.Contains(low, "no content/tool_calls") ||
		strings.Contains(low, "no client-visible content") ||
		strings.Contains(low, "empty_upstream") ||
		strings.Contains(low, "empty upstream")
}

func isGrokEmptyModelOutputCode(code string) bool {
	c := strings.ToLower(strings.TrimSpace(code))
	if c == "" {
		return false
	}
	return c == "empty_upstream" ||
		c == "empty-model-output" ||
		c == "empty_model_output" ||
		strings.Contains(c, "empty_upstream") ||
		strings.Contains(c, "empty-model-output")
}

func grokFreeUsageCooldownDuration(low string) time.Duration {
	// rolling 24-hour 描述上游用量窗口，不代表从代理观察到 429 后冷却 24 小时；
	// 缺少绝对重置时间时使用短探测间隔，由成功探测解除阻断。
	return grokFreeUsageProbeCooldown
}

const grokFreeUsageProbeCooldown = 10 * time.Minute

func parseGrokTokenPair(errText string) (actual, limit int64, ok bool) {
	m := reGrokTokenPair.FindStringSubmatch(errText)
	if len(m) != 3 {
		return 0, 0, false
	}
	a, errA := strconv.ParseInt(m[1], 10, 64)
	b, errB := strconv.ParseInt(m[2], 10, 64)
	if errA != nil || errB != nil {
		return 0, 0, false
	}
	return a, b, true
}

func extractGrokFailureModel(text string, responseBody []byte, fallback string) string {
	if m := reGrokModelFor.FindStringSubmatch(text); len(m) == 2 {
		return normalizeGrokFailureModelID(m[1])
	}
	if len(responseBody) > 0 {
		if m := strings.TrimSpace(firstNonEmpty(
			gjson.GetBytes(responseBody, "error.model").String(),
			gjson.GetBytes(responseBody, "model").String(),
		)); m != "" {
			return normalizeGrokFailureModelID(m)
		}
	}
	return normalizeGrokFailureModelID(fallback)
}

// normalizeGrokFailureModelID 移除模型提取结果中的空白和句尾标点。
func normalizeGrokFailureModelID(model string) string {
	model = strings.TrimSpace(model)
	model = strings.TrimRight(model, ".,;:!?")
	return strings.TrimSpace(model)
}

// applyGrokUpstreamFailureDecision 将分类结果映射到账户健康状态；返回 true 表示已完整处理，
// 调用方不能再次应用状态码默认逻辑。
func (s *OpenAIGatewayService) applyGrokUpstreamFailureDecision(
	ctx context.Context,
	account *Account,
	decision GrokUpstreamFailureDecision,
) bool {
	if s == nil || account == nil || !decision.ShouldCooldown || decision.Cooldown <= 0 {
		return false
	}
	// 原因保持简短稳定，供运维界面和 temp_unschedulable_reason 使用。
	var reason string
	switch decision.Class {
	case GrokFailureFreeUsage:
		reason = "grok free usage exhausted"
		// 模型级免费额度耗尽只软封禁该模型，使同账号其它模型仍可调度。
		low := strings.ToLower(decision.Reason)
		if decision.Model != "" && isGrokModelSpecificFreeUsage(low, decision.Model) {
			until := time.Now().Add(decision.Cooldown)
			markGrokModelQuotaBlock(account.ID, decision.Model, until)
			// 上游已明确限定到单模型，账号级冷却会错误移除健康的其它模型。
			return true
		}
	case GrokFailureBilling:
		low := strings.ToLower(decision.Reason)
		if strings.Contains(low, "spending") || strings.Contains(low, "credits") {
			// 消费上限或 credits 耗尽属于账单窗口条件，保留账号可恢复状态并由常规限流恢复解除。
			s.rateLimitGrok(ctx, account, grokSpendingLimitResetAt(account, time.Now()))
			return true
		}
		// 保留历史 402/payment 原因，兼容运维界面和回归测试。
		reason = "grok payment required"
	case GrokFailureEmptyUpstream:
		reason = "grok empty model output"
	case GrokFailureModelCapacity:
		if persistGrokTransientModelCooldown(account, decision) {
			return true
		}
		reason = "grok model capacity"
	case GrokFailureRateLimit:
		// 不含免费额度语义的纯 429 继续走 Retry-After 与额度请求头快照路径。
		return false
	case GrokFailureServer:
		reason = "grok upstream temporary error"
	default:
		return false
	}
	s.tempUnscheduleGrok(ctx, account, decision.Cooldown, reason)
	return true
}

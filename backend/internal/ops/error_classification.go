package ops

import (
	"strings"
)

// ErrorPhaseAccountAuth 保留供应商账号认证失败的观测阶段值。
const ErrorPhaseAccountAuth = "account_auth"

// isKnownOpsErrorType returns true if t is a recognized error type used by the
// ops classification pipeline.  Upstream proxies sometimes return garbage values
// (e.g. the Go-serialized literal "<nil>") which would pollute phase/severity
// classification if accepted blindly.
func isKnownOpsErrorType(t string) bool {
	switch t {
	case "invalid_request_error",
		"authentication_error",
		"permission_error",
		"model_not_found",
		"service_unavailable",
		"rate_limit_error",
		"billing_error",
		"subscription_error",
		"upstream_error",
		"overloaded_error",
		"service_unavailable_error",
		"api_error",
		"not_found_error",
		"forbidden_error":
		return true
	}
	return false
}

// NormalizeErrorType 保留已知类别和原错误码回退顺序。
func NormalizeErrorType(errType string, code string, message ...string) string {
	// 本地模型能力与会话隔离限制虽然由权限层返回，但对 Ops 来说属于
	// 客户端请求错误，沿用历史 api_error 分类；其它权限拒绝保持原类型。
	msg := ""
	if len(message) > 0 {
		msg = strings.ToLower(strings.TrimSpace(message[0]))
	}
	if errType == "permission_error" &&
		(strings.Contains(msg, "the current group does not support the requested model") ||
			strings.Contains(msg, "this session already belongs to another group and cannot switch to the current session-isolated group")) {
		return "api_error"
	}
	if errType != "" && isKnownOpsErrorType(errType) {
		return errType
	}
	switch strings.TrimSpace(code) {
	case opsCodeInsufficientBalance:
		return "billing_error"
	case opsCodeUsageLimitExceeded, opsCodeSubscriptionNotFound, opsCodeSubscriptionInvalid:
		return "subscription_error"
	default:
		return "api_error"
	}
}

func classifyOpsPhase(errType, message, code string) string {
	msg := strings.ToLower(message)
	// 标准阶段：request|auth|account_auth|routing|upstream|network|internal。
	// Map billing/concurrency/response => request; scheduling => routing.
	if isOpsClientAuthError(code, msg) {
		return "auth"
	}
	if isOpsLocalBusinessLimitError(code, msg) {
		return "request"
	}

	switch errType {
	case "authentication_error":
		return "auth"
	case "billing_error", "subscription_error":
		return "request"
	case "rate_limit_error":
		if strings.Contains(msg, "concurrency") || strings.Contains(msg, "pending") || strings.Contains(msg, "queue") {
			return "request"
		}
		return "upstream"
	case "invalid_request_error", "permission_error", "forbidden_error", "not_found_error", "model_not_found":
		return "request"
	case "upstream_error", "overloaded_error":
		return "upstream"
	case "api_error":
		if IsNoAvailableAccountMessage(msg) {
			return "routing"
		}
		return "internal"
	default:
		return "internal"
	}
}

// ClassifySeverity 按原错误类别和状态码返回告警严重性。
func ClassifySeverity(errType string, status int) string {
	switch errType {
	case "invalid_request_error", "authentication_error", "permission_error", "forbidden_error", "not_found_error", "model_not_found", "billing_error", "subscription_error":
		return "P3"
	}
	if status >= 500 {
		return "P1"
	}
	if status == 429 {
		return "P1"
	}
	if status >= 400 {
		return "P2"
	}
	return "P3"
}

// ClassifyRequestError 按原优先级组合本地与上游观测，生成唯一的统计分类。
func ClassifyRequestError(input ErrorClassificationInput) (phase string, isBusinessLimited bool, errorOwner string, errorSource string) {
	errType, message, code, status := input.Type, input.Message, input.Code, input.Status
	phase = classifyOpsPhase(errType, message, code)
	routingCapacityLimited := input.RoutingCapacityLimited
	clientBusinessLimited := input.ClientBusinessLimited
	localModelConfiguration := clientBusinessLimited && input.LocalModelConfiguration
	upstreamError := input.UpstreamError
	upstreamClientInvalidRequest := input.UpstreamClientInvalidRequest
	accountAuthFailure := input.AccountAuthFailure
	if localModelConfiguration {
		phase = "routing"
	} else if accountAuthFailure && !routingCapacityLimited {
		phase = "account_auth"
	} else if upstreamClientInvalidRequest && !routingCapacityLimited {
		phase = "request"
	} else if upstreamError && !routingCapacityLimited {
		phase = "upstream"
	}
	if clientBusinessLimited && !upstreamError && !routingCapacityLimited && !localModelConfiguration {
		phase = "auth"
	}
	if routingCapacityLimited {
		phase = "routing"
	}
	msg := strings.ToLower(message)
	effectiveUpstreamError := upstreamError && !localModelConfiguration
	localClientAuthError := !effectiveUpstreamError && phase == "auth" && isOpsClientAuthError(code, msg)
	localBusinessLimited := !effectiveUpstreamError && classifyOpsIsBusinessLimited(errType, phase, code, status, message, localClientAuthError)
	isBusinessLimited = localModelConfiguration || routingCapacityLimited || upstreamClientInvalidRequest ||
		(clientBusinessLimited && !effectiveUpstreamError) || localBusinessLimited
	errorOwner = classifyOpsErrorOwner(phase, message)
	errorSource = classifyOpsErrorSource(phase, message)
	return phase, isBusinessLimited, errorOwner, errorSource
}

func classifyOpsIsBusinessLimited(errType, phase, code string, status int, message string, localClientAuthError ...bool) bool {
	if len(localClientAuthError) > 0 && localClientAuthError[0] {
		return true
	}
	if isOpsLocalBusinessLimitError(code, strings.ToLower(message)) {
		return true
	}
	if phase == "billing" || phase == "concurrency" {
		// SLA/错误率排除“用户级业务限制”
		return true
	}
	// Avoid treating upstream rate limits as business-limited.
	if errType == "rate_limit_error" && strings.Contains(strings.ToLower(message), "upstream") {
		return false
	}
	_ = status
	return false
}

func isOpsClientAuthError(code string, msg string) bool {
	switch strings.TrimSpace(code) {
	case opsCodeInvalidAPIKey,
		opsCodeAPIKeyRequired,
		opsCodeAPIKeyExpired,
		opsCodeAPIKeyDisabled,
		opsCodeUserNotFound,
		opsCodeUserInactive,
		opsCodeGroupDeleted,
		opsCodeGroupDisabled:
		return true
	}
	return strings.Contains(msg, "invalid api key") ||
		strings.Contains(msg, "api key is required") ||
		strings.Contains(msg, "api key is disabled") ||
		strings.Contains(msg, "user associated with api key not found") ||
		strings.Contains(msg, "user account is not active") ||
		strings.Contains(msg, "api key 所属分组已删除") ||
		strings.Contains(msg, "api key 所属分组已停用") ||
		strings.Contains(msg, "api key is not assigned to any group")
}

func isOpsLocalBusinessLimitError(code string, msg string) bool {
	switch strings.TrimSpace(code) {
	case opsCodeInsufficientBalance,
		opsCodeUsageLimitExceeded,
		opsCodeSubscriptionNotFound,
		opsCodeSubscriptionInvalid,
		opsCodeAPIKeyQuotaExhausted,
		opsCodeAPIKeyQueryDeprecated:
		return true
	}
	return strings.Contains(msg, "api key in query parameter is deprecated") ||
		strings.Contains(msg, "query parameter api_key is deprecated") ||
		strings.Contains(msg, "no active subscription found for this group") ||
		strings.Contains(msg, "subscription is invalid or expired") ||
		strings.Contains(msg, opsErrInsufficientBalance) ||
		strings.Contains(msg, "insufficient account balance") ||
		strings.Contains(msg, "api key group platform is not gemini") ||
		strings.Contains(msg, "api key 额度已用完") ||
		strings.Contains(msg, "api key 5小时限额已用完") ||
		strings.Contains(msg, "api key 日限额已用完") ||
		strings.Contains(msg, "api key 7天限额已用完") ||
		strings.Contains(msg, "daily usage limit exceeded") ||
		strings.Contains(msg, "weekly usage limit exceeded") ||
		strings.Contains(msg, "monthly usage limit exceeded") ||
		strings.Contains(msg, "usage quota exhausted for this platform") ||
		strings.Contains(msg, "requests-per-minute limit exceeded") ||
		strings.Contains(msg, "too many pending requests") ||
		strings.Contains(msg, "concurrency limit exceeded") ||
		strings.Contains(msg, "image generation concurrency limit exceeded") ||
		strings.Contains(msg, "this group is restricted to claude code clients") ||
		strings.Contains(msg, "this group does not allow /v1/messages dispatch") ||
		strings.Contains(msg, "image generation is not enabled for this group") ||
		(strings.Contains(msg, "reasoning effort") && strings.Contains(msg, "exceeds this group's limit")) ||
		strings.Contains(msg, "token counting is not supported for this platform") ||
		strings.Contains(msg, "images api is not supported for this platform") ||
		strings.Contains(msg, "the current group does not support the requested model") ||
		(strings.Contains(msg, "model ") && strings.Contains(msg, " not in whitelist")) ||
		(strings.Contains(msg, "beta feature ") && strings.Contains(msg, " is not allowed")) ||
		(strings.Contains(msg, "openai service_tier=") && strings.Contains(msg, " is not allowed for model")) ||
		// 本地客户端策略与会话隔离拒绝属于用户配置限制，不计入 SLA。
		strings.Contains(msg, "this account only allows codex official clients") ||
		strings.Contains(msg, "this account only allows clients matched by the configured tls router") ||
		strings.Contains(msg, "this account only allows configured openai oauth clients") ||
		strings.Contains(msg, "this session already belongs to another group and cannot switch to the current session-isolated group") ||
		strings.Contains(msg, "openai wsv1 is temporarily unsupported") ||
		strings.Contains(msg, "openai codex passthrough requires a non-empty instructions field")
}

// IsNoAvailableAccountMessage 识别原路由容量提示，供 HTTP 观测标记复用。
func IsNoAvailableAccountMessage(message string) bool {
	msg := strings.ToLower(message)
	return strings.Contains(msg, opsErrNoAvailableAccounts) ||
		strings.Contains(msg, "no available account") ||
		strings.Contains(msg, "no available gemini accounts") ||
		strings.Contains(msg, "no available openai accounts") ||
		strings.Contains(msg, "no available compatible accounts")
}

func classifyOpsErrorOwner(phase string, message string) string {
	// Standardized owners: client|provider|platform
	switch phase {
	case "upstream", "network":
		return "provider"
	case "account_auth":
		return "provider"
	case "request", "auth":
		return "client"
	case "routing", "internal":
		return "platform"
	default:
		if strings.Contains(strings.ToLower(message), "upstream") {
			return "provider"
		}
		return "platform"
	}
}

func classifyOpsErrorSource(phase string, message string) string {
	// Standardized sources: client_request|upstream_http|gateway
	switch phase {
	case "upstream":
		return "upstream_http"
	case "account_auth":
		return "gateway"
	case "network":
		return "gateway"
	case "request", "auth":
		return "client_request"
	case "routing", "internal":
		return "gateway"
	default:
		if strings.Contains(strings.ToLower(message), "upstream") {
			return "upstream_http"
		}
		return "gateway"
	}
}

const (
	opsErrNoAvailableAccounts    = "no available accounts"
	opsErrInsufficientBalance    = "insufficient balance"
	opsCodeInsufficientBalance   = "INSUFFICIENT_BALANCE"
	opsCodeUsageLimitExceeded    = "USAGE_LIMIT_EXCEEDED"
	opsCodeSubscriptionNotFound  = "SUBSCRIPTION_NOT_FOUND"
	opsCodeSubscriptionInvalid   = "SUBSCRIPTION_INVALID"
	opsCodeUserInactive          = "USER_INACTIVE"
	opsCodeInvalidAPIKey         = "INVALID_API_KEY"
	opsCodeAPIKeyRequired        = "API_KEY_REQUIRED"
	opsCodeAPIKeyExpired         = "API_KEY_EXPIRED"
	opsCodeAPIKeyDisabled        = "API_KEY_DISABLED"
	opsCodeUserNotFound          = "USER_NOT_FOUND"
	opsCodeAPIKeyQuotaExhausted  = "API_KEY_QUOTA_EXHAUSTED"
	opsCodeAPIKeyQueryDeprecated = "api_key_in_query_deprecated"
	opsCodeGroupDeleted          = "GROUP_DELETED"
	opsCodeGroupDisabled         = "GROUP_DISABLED"
)

// ErrorClassificationInput 仅固化 HTTP 层观测到的事实；分类核心不读取 Gin 或可变请求状态。
type ErrorClassificationInput struct {
	Type, Message, Code                                                    string
	Status                                                                 int
	RoutingCapacityLimited, ClientBusinessLimited, LocalModelConfiguration bool
	UpstreamError, UpstreamClientInvalidRequest, AccountAuthFailure        bool
}

package provider

import (
	"errors"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

func IsAmbiguousGrokEntitlementRefreshError(value *account.Record, err error) bool {
	if value == nil || !value.IsGrokOAuth() || err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	coarseEntitlementLabel := strings.EqualFold(apperror.Reason(err), "GROK_OAUTH_ENTITLEMENT_DENIED") ||
		strings.Contains(msg, "grok_oauth_entitlement_denied")
	if !coarseEntitlementLabel {
		return false
	}

	for _, evidence := range []string{
		"subscription required",
		"no active grok subscription",
		"no active subscription",
		"grok subscription required",
		"account is not entitled",
		"not entitled",
		"entitlement required",
		"subscription inactive",
		"subscription expired",
		"upgrade your plan",
	} {
		if strings.Contains(msg, evidence) {
			return false
		}
	}
	if bodyIndex := strings.Index(msg, "body:"); bodyIndex >= 0 {
		body := msg[bodyIndex+len("body:"):]
		for _, evidence := range []string{
			"entitlement_denied",
			"entitlement denied",
			"subscription_required",
			"no_active_subscription",
		} {
			if strings.Contains(body, evidence) {
				return false
			}
		}
	}
	return true
}

func IsSharedProviderRefreshError(err error) bool {
	if err == nil {
		return false
	}
	var qoderOpenAPIErr *qoder.OpenAPIError
	if errors.As(err, &qoderOpenAPIErr) && qoderOpenAPIErr.InvalidCredentials() {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"invalid_client",
		"unauthorized_client",
		"invalid_scope",
		"unknown scope",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// IsNonRetryableRefreshError 判断是否为不可重试的刷新错误
// 这些错误通常表示凭证已失效或配置确实缺失，需要用户重新授权
// 注意：missing_project_id 错误只在真正缺失（从未获取过）时返回，临时获取失败不会返回此错误
func IsNonRetryableRefreshError(err error) bool {
	if err == nil {
		return false
	}
	var qoderOpenAPIErr *qoder.OpenAPIError
	if errors.As(err, &qoderOpenAPIErr) && qoderOpenAPIErr.InvalidCredentials() {
		return true
	}
	msg := strings.ToLower(err.Error())
	nonRetryable := []string{
		"invalid_grant",                       // refresh_token 已失效
		"invalid_refresh_token",               // refresh_token 无效，team 账号工作区被删除时会出现
		"token_expired",                       // OpenAI refresh_token 已过期，需要重新授权
		"app_session_terminated",              // OpenAI app session 被终止，需要重新授权
		"invalid_client",                      // 客户端配置错误
		"unauthorized_client",                 // 客户端未授权
		"access_denied",                       // 访问被拒绝
		"refresh_token_reused",                // OpenAI refresh_token 已被消费，需要重新授权
		"refresh_token_invalidated",           // OpenAI session 结束导致 refresh_token 被废止
		"refresh token has already been used", // 兼容错误体未透出 code 的情况
		"missing_project_id",                  // 缺少 project_id
		"no refresh token available",
		"grok_oauth_entitlement_denied",
		"entitlement_denied",
		"invalid_scope",
		"unknown scope",
		"subscription required",
		"no active grok subscription",
	}
	for _, needle := range nonRetryable {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

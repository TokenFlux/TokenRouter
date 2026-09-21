package forward

import (
	"errors"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

const (
	GrokCredentialUnavailableClientMessage                      = "No healthy Grok OAuth account is currently available"
	GrokCredentialReasonRevoked            GatewayFailureReason = "grok_oauth_credential_revoked"
	GrokCredentialReasonMissing            GatewayFailureReason = "grok_oauth_credentials_missing"
	GrokCredentialReasonEntitlement        GatewayFailureReason = "grok_oauth_entitlement_action_required"
	GrokCredentialReasonProxyInvalid       GatewayFailureReason = "grok_oauth_proxy_invalid"
	GrokCredentialReasonRefreshTransient   GatewayFailureReason = "grok_oauth_refresh_transient"
	GrokCredentialReasonProviderConfig     GatewayFailureReason = "grok_oauth_provider_config"
	GrokCredentialReasonProviderDown       GatewayFailureReason = "grok_oauth_provider_unavailable"
	GrokCredentialReasonAccountChanged     GatewayFailureReason = "grok_oauth_account_state_changed"
	GrokCredentialReasonStateUpdate        GatewayFailureReason = "grok_oauth_account_state_update_failed"
	GrokCredentialReasonFailoverTimeout    GatewayFailureReason = "grok_oauth_failover_timeout"
)

// GrokCredentialFailure 是本次凭据获取的分类，不序列化凭据或控制状态。
type GrokCredentialFailure struct {
	Scope     GatewayFailureScope  `json:"-"`
	Reason    GatewayFailureReason `json:"-"`
	Action    NextAccountAction    `json:"-"`
	Permanent bool                 `json:"-"`
	Transient bool                 `json:"-"`
	Message   string               `json:"-"`
	snapshot  *accountcore.CredentialMutationSnapshot
}

// ClassifyGrokCredentialFailure 保留原因匹配顺序，仅读取代理存在性与错误链。
func ClassifyGrokCredentialFailure(hasProxy bool, err error) GrokCredentialFailure {
	stableReason := strings.ToLower(strings.TrimSpace(apperror.Reason(err)))
	message := ""
	if err != nil {
		message = strings.ToLower(err.Error())
	}
	contains := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(stableReason, value) || strings.Contains(message, value) {
				return true
			}
		}
		return false
	}
	var providerConfigErr *accountcore.ProviderConfigurationRefreshError
	var containmentErr *accountcore.ProviderCycleContainmentRefreshError

	switch {
	case errors.Is(err, accountcore.ErrGrokOAuthRefreshTokenMissing), errors.Is(err, accountcore.ErrGrokOAuthAccessTokenMissing), errors.Is(err, accountcore.ErrGrokOAuthAccessTokenExpired):
		return GrokCredentialFailure{Scope: GatewayFailureScopeAccount, Reason: GrokCredentialReasonMissing, Action: NextAccountRetry, Permanent: true, Message: "Grok OAuth credentials are missing or expired"}
	case contains("invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_reused", "refresh_token_invalidated", "app_session_terminated"):
		return GrokCredentialFailure{Scope: GatewayFailureScopeAccount, Reason: GrokCredentialReasonRevoked, Action: NextAccountRetry, Permanent: true, Message: "Grok OAuth credentials require account action"}
	case contains("spending limit", "run out of credits", "out of credits", "credits exhausted", "included free usage"):
		// 账单限额与滚动免费额度耗尽无需更换 OAuth 凭证即可恢复。
		// 将刷新失败视为临时故障，使账号之后仍可再次探测额度。
		return GrokCredentialFailure{Scope: GatewayFailureScopeAccount, Reason: GrokCredentialReasonRefreshTransient, Action: NextAccountRetry, Transient: true, Message: "Grok OAuth billing quota is temporarily exhausted"}
	case contains("grok_oauth_entitlement_denied", "entitlement_denied", "access_denied", "subscription required", "no active grok subscription"):
		return GrokCredentialFailure{Scope: GatewayFailureScopeAccount, Reason: GrokCredentialReasonEntitlement, Action: NextAccountRetry, Permanent: true, Message: "Grok OAuth entitlement requires account action"}
	case errors.Is(err, accountcore.ErrGrokOAuthConfiguredProxyMiss), contains("grok_oauth_proxy_not_found"):
		return GrokCredentialFailure{Scope: GatewayFailureScopeAccount, Reason: GrokCredentialReasonProxyInvalid, Action: NextAccountRetry, Permanent: true, Message: "Grok OAuth account proxy configuration is invalid"}
	case errors.Is(err, accountcore.ErrRefreshAccountRereadFailed):
		return GrokCredentialFailure{Scope: GatewayFailureScopeProvider, Reason: GrokCredentialReasonProviderDown, Action: NextAccountStop, Message: "Grok OAuth account state is temporarily unavailable"}
	case errors.Is(err, accountcore.ErrRefreshCredentialPersist):
		return GrokCredentialFailure{Scope: GatewayFailureScopeProvider, Reason: GrokCredentialReasonProviderDown, Action: NextAccountStop, Message: "Grok OAuth shared credential state is temporarily unavailable"}
	case errors.As(err, &containmentErr):
		return GrokCredentialFailure{Scope: GatewayFailureScopeProvider, Reason: GrokCredentialReasonProviderDown, Action: NextAccountStop, Message: "Grok OAuth provider state is temporarily unavailable"}
	case errors.Is(err, accountcore.ErrRefreshAccountStateChanged):
		return GrokCredentialFailure{Scope: GatewayFailureScopeAccount, Reason: GrokCredentialReasonAccountChanged, Action: NextAccountRetry, Message: "Grok OAuth account eligibility changed"}
	case errors.As(err, &providerConfigErr):
		return GrokCredentialFailure{Scope: GatewayFailureScopeProvider, Reason: GrokCredentialReasonProviderConfig, Action: NextAccountStop, Message: "Grok OAuth provider configuration is unavailable"}
	case errors.Is(err, accountcore.ErrGrokOAuthRefreshNotConfigured), contains("invalid_client", "unauthorized_client", "invalid_scope", "unknown scope", "grok oauth service is not configured", "grok_oauth_proxy_not_available"):
		return GrokCredentialFailure{Scope: GatewayFailureScopeProvider, Reason: GrokCredentialReasonProviderConfig, Action: NextAccountStop, Message: "Grok OAuth provider configuration is unavailable"}
	case contains("grok_oauth_proxy_lookup_failed"),
		contains("grok_oauth_token_refresh_failed") && contains("status 403") && !hasProxy:
		return GrokCredentialFailure{Scope: GatewayFailureScopeProvider, Reason: GrokCredentialReasonProviderDown, Action: NextAccountStop, Message: "Grok OAuth provider is temporarily unavailable"}
	case contains("grok_oauth_client_init_failed") && !hasProxy:
		return GrokCredentialFailure{Scope: GatewayFailureScopeProvider, Reason: GrokCredentialReasonProviderConfig, Action: NextAccountStop, Message: "Grok OAuth provider configuration is unavailable"}
	case contains("grok_oauth_request_failed") && !hasProxy:
		return GrokCredentialFailure{Scope: GatewayFailureScopeProvider, Reason: GrokCredentialReasonProviderDown, Action: NextAccountStop, Message: "Grok OAuth provider is temporarily unavailable"}
	case contains("status 429", "status 500", "status 502", "status 503", "status 504") && !hasProxy:
		return GrokCredentialFailure{Scope: GatewayFailureScopeProvider, Reason: GrokCredentialReasonProviderDown, Action: NextAccountStop, Message: "Grok OAuth provider is temporarily unavailable"}
	default:
		return GrokCredentialFailure{Scope: GatewayFailureScopeAccount, Reason: GrokCredentialReasonRefreshTransient, Action: NextAccountRetry, Transient: true, Message: "Grok OAuth credential refresh is temporarily unavailable"}
	}
}

// SetSnapshot 绑定本次持久化比较快照，保持私有且不参与 JSON。
func (f *GrokCredentialFailure) SetSnapshot(snapshot *accountcore.CredentialMutationSnapshot) {
	f.snapshot = snapshot
}

// Snapshot 只供本次受控状态写入使用，不作为公开诊断载荷。
func (f GrokCredentialFailure) Snapshot() *accountcore.CredentialMutationSnapshot { return f.snapshot }

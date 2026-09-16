// 本文件只投影旧账号、egress 和动态设置，原生包拥有请求字节构造。
package service

import (
	"context"
	"fmt"
	"net/http"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/gin-gonic/gin"
)

func (s *GatewayService) anthropicRequestOptions(ctx context.Context, c *gin.Context, account *Account, model, tokenType string, mimic bool) claude.RequestOptions {
	o := claude.RequestOptions{InjectAPIKeyBeta: s.cfg != nil && s.cfg.Gateway.InjectBetaForAPIKey, AccountID: account.ID, OAuth: account.IsOAuth(), AccountUUID: account.GetExtraString("account_uuid"), MaskSession: account.IsSessionIDMaskingEnabled(), APIKeyBearer: account.GetAnthropicAPIKeyAuthScheme() == AnthropicAPIKeyAuthSchemeAuthorizationBearer, ClientHeaders: http.Header{}, ApplyOverrides: account.ApplyHeaderOverrides}
	if c != nil && c.Request != nil {
		o.ClientHeaders = c.Request.Header
	}
	if s.identityService != nil {
		o.Fingerprint = s.identityService.RequestFingerprint
	}
	o.URL = func() (string, error) {
		url := claudeAPIURL
		if account.Type == AccountTypeAPIKey {
			if base := account.GetBaseURL(); base != "" {
				validated, err := s.validateUpstreamBaseURL(base)
				if err != nil {
					return "", err
				}
				url = validated + "/v1/messages?beta=true"
			}
		} else if account.IsCustomBaseURLEnabled() {
			custom := account.GetCustomBaseURL()
			if custom == "" {
				return "", fmt.Errorf("custom_base_url is enabled but not configured for account %d", account.ID)
			}
			validated, err := s.validateUpstreamBaseURL(custom)
			if err != nil {
				return "", err
			}
			url = s.buildCustomRelayURL(validated, "/v1/messages", account)
		}
		return url, nil
	}
	o.FastMode = func(ctx context.Context, body []byte, h http.Header) ([]byte, http.Header, error) {
		return s.applyClaudeAPIKeyFastMode(ctx, account, model, body, h)
	}
	o.Forwarding = func(ctx context.Context) (bool, bool) {
		if s.settingService == nil {
			return true, false
		}
		fp, mpt, _ := s.settingService.GetGatewayForwardingSettings(ctx)
		return fp, mpt
	}
	o.FilterSet = func(ctx context.Context) map[string]struct{} { return s.getBetaPolicyFilterSet(ctx, c, account, model) }
	o.BetaOverride = func() (string, bool) { return account.HeaderOverrideValue("anthropic-beta") }
	o.CheckFastBeta = func(ctx context.Context) error {
		if err := s.checkBetaPolicyBlockForTokens(ctx, []string{claude.BetaFastMode}, account, model); err != nil {
			return err
		}
		return nil
	}
	o.Debug = func(req *http.Request, body []byte, values map[string]string) {
		s.debugLogGatewaySnapshot("UPSTREAM_FORWARD", req.Header, body, values)
	}
	o.Capture = func(req *http.Request, body []byte) {
		if c != nil && tokenType == "oauth" {
			c.Set(claudeMimicDebugInfoKey, buildClaudeMimicDebugLine(req, body, account, tokenType, mimic))
		}
		if s.debugClaudeMimicEnabled() {
			logClaudeMimicDebug(req, body, account, tokenType, mimic)
		}
	}
	return o
}

// count_tokens 只改变原端点选择，保留代理参数与动态设置的原调用时机。
func (s *GatewayService) countTokensRequestOptions(ctx context.Context, c *gin.Context, value *Account, model, tokenType string, mimic, passthrough bool) claude.RequestOptions {
	options := s.anthropicRequestOptions(ctx, c, value, model, tokenType, mimic)
	options.URL = func() (string, error) {
		target := claudeAPICountTokensURL
		if passthrough || value.Type == AccountTypeAPIKey {
			if base := value.GetBaseURL(); base != "" {
				validated, err := s.validateUpstreamBaseURL(base)
				if err != nil {
					return "", err
				}
				target = validated + "/v1/messages/count_tokens?beta=true"
			}
		} else if value.IsCustomBaseURLEnabled() {
			custom := value.GetCustomBaseURL()
			if custom == "" {
				return "", fmt.Errorf("custom_base_url is enabled but not configured for account %d", value.ID)
			}
			validated, err := s.validateUpstreamBaseURL(custom)
			if err != nil {
				return "", err
			}
			target = s.buildCustomRelayURL(validated, "/v1/messages/count_tokens", value)
		}
		return target, nil
	}
	return options
}

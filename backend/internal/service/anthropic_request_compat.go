// 本文件只投影旧账号、egress 和动态设置，原生包拥有请求字节构造。
package service

import (
	"context"
	"fmt"
	"net/http"

	accountmodule "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/gin-gonic/gin"
)

func (s *GatewayService) anthropicRequestOptions(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, model, tokenType string, mimic bool) claude.RequestOptions {
	o := claude.RequestOptions{InjectAPIKeyBeta: s.cfg != nil && s.cfg.Gateway.InjectBetaForAPIKey, AccountID: account.Record.ID, OAuth: account.View().IsOAuth(), AccountUUID: account.View().GetExtraString("account_uuid"), MaskSession: account.View().IsSessionIDMaskingEnabled(), APIKeyBearer: gatewayprovider.ExecutionProtocolRecord(account).GetAnthropicAPIKeyAuthScheme() == accountmodule.AnthropicAPIKeyAuthSchemeAuthorizationBearer, ClientHeaders: http.Header{}, ApplyOverrides: bindAccountHeaders(account)}
	if c != nil && c.Request != nil {
		o.ClientHeaders = c.Request.Header
	}
	if s.identityService != nil {
		o.Fingerprint = s.identityService
	}
	o.URL = func() (string, error) {
		url := claude.ClaudeAPIURL
		if account.Record.Type == capability.AccountTypeAPIKey {
			if base := account.View().GetBaseURL(); base != "" {
				validated, err := s.validateUpstreamBaseURL(base)
				if err != nil {
					return "", err
				}
				url = validated + "/v1/messages?beta=true"
			}
		} else if account.View().IsCustomBaseURLEnabled() {
			custom := account.View().GetCustomBaseURL()
			if custom == "" {
				return "", fmt.Errorf("custom_base_url is enabled but not configured for account %d", account.Record.ID)
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
		fp, mpt, _ := s.settingService.Gateway.GetGatewayForwardingSettings(ctx)
		return fp, mpt
	}
	o.FilterSet = func(ctx context.Context) map[string]struct{} { return s.getBetaPolicyFilterSet(ctx, c, account, model) }
	o.BetaOverride = func() (string, bool) {
		return accountprovider.HeaderOverrideValue(gatewayprovider.ExecutionProtocolRecord(account), "anthropic-beta")
	}
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
func (s *GatewayService) countTokensRequestOptions(ctx context.Context, c *gin.Context, value *gatewayprovider.ExecutionAccount, model, tokenType string, mimic, passthrough bool) claude.RequestOptions {
	options := s.anthropicRequestOptions(ctx, c, value, model, tokenType, mimic)
	options.URL = func() (string, error) {
		target := claude.ClaudeAPICountTokensURL
		if passthrough || value.Record.Type == capability.AccountTypeAPIKey {
			if base := value.View().GetBaseURL(); base != "" {
				validated, err := s.validateUpstreamBaseURL(base)
				if err != nil {
					return "", err
				}
				target = validated + "/v1/messages/count_tokens?beta=true"
			}
		} else if value.View().IsCustomBaseURLEnabled() {
			custom := value.View().GetCustomBaseURL()
			if custom == "" {
				return "", fmt.Errorf("custom_base_url is enabled but not configured for account %d", value.Record.ID)
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

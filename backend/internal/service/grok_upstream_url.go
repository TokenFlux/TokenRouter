package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	"github.com/TokenFlux/TokenRouter/internal/util/urlvalidator"
)

func grokBaseURLValidator(account *Account, cfg *config.Config) (xai.BaseURLValidator, error) {
	if account == nil || !account.IsGrok() {
		return nil, fmt.Errorf("grok account is required")
	}
	switch account.Type {
	case AccountTypeOAuth:
		// 即使运营方启用了严格白名单，官方网关也始终受信任并可用；
		// 自定义转发主机则必须通过与 API-key 账号相同的运营方策略。
		// 此处直接按主机区分官方与自定义地址，避免 ValidateTrustedBaseURL 在
		// XAI_ALLOW_UNSAFE_URL_OVERRIDES 调试开关下放宽校验，导致 OAuth 凭据泄露到任意主机。
		policyValidator := grokOperatorPolicyValidator(cfg)
		return redactedGrokBaseURLValidator(func(raw string) (string, error) {
			if xai.IsOfficialBaseURL(raw) {
				return xai.ValidateTrustedBaseURL(raw)
			}
			return policyValidator(raw)
		}), nil
	case AccountTypeAPIKey:
		return redactedGrokBaseURLValidator(grokOperatorPolicyValidator(cfg)), nil
	default:
		return nil, fmt.Errorf("unsupported grok account type: %s", account.Type)
	}
}

// grokOperatorPolicyValidator 按全局出站 URL 安全策略校验自定义 base_url：
// 白名单开启时强制 UpstreamHosts；关闭时仅做格式校验（HTTP 允许与否跟随配置）。
func grokOperatorPolicyValidator(cfg *config.Config) xai.BaseURLValidator {
	if cfg == nil {
		return xai.ValidateBaseURL
	}
	if !cfg.Security.URLAllowlist.Enabled {
		return func(raw string) (string, error) {
			return urlvalidator.ValidateURLFormat(raw, cfg.Security.URLAllowlist.AllowInsecureHTTP)
		}
	}
	return func(raw string) (string, error) {
		return urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{
			AllowedHosts:     cfg.Security.URLAllowlist.UpstreamHosts,
			RequireAllowlist: true,
			AllowPrivate:     cfg.Security.URLAllowlist.AllowPrivateHosts,
		})
	}
}

func redactedGrokBaseURLValidator(validator xai.BaseURLValidator) xai.BaseURLValidator {
	return func(raw string) (string, error) {
		validated, err := validator(raw)
		if err != nil {
			return "", errors.New("base URL rejected by URL security policy")
		}
		return validated, nil
	}
}

func buildGrokResponsesURL(account *Account, cfg *config.Config, settings ...*SettingService) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	baseURL := account.GetGrokBaseURL()
	if len(settings) > 0 && settings[0] != nil {
		baseURL = settings[0].ResolveGrokBaseURL(context.Background(), account)
	}
	return xai.BuildResponsesURLWithValidator(baseURL, validator)
}

func buildGrokChatCompletionsURL(account *Account, cfg *config.Config, settings ...*SettingService) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	baseURL := account.GetGrokBaseURL()
	if len(settings) > 0 && settings[0] != nil {
		baseURL = settings[0].ResolveGrokBaseURL(context.Background(), account)
	}
	return xai.BuildChatCompletionsURLWithValidator(baseURL, validator)
}

// buildGrokBillingURL 解析 billing 探测端点：跟随账号的转发 base_url，
// 未定制的账号仍指向官方 CLI 网关。
func buildGrokBillingURL(account *Account, cfg *config.Config, weekly bool) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	baseURL := account.GetGrokBaseURL()
	// 官方公共或区域 API 主机不提供 Grok Build 账单接口。
	// 自定义中继可能同时代理推理与 CLI 账单路径，因此继续使用其配置主机。
	if xai.IsOfficialBaseURL(baseURL) && !isGrokCLIProxyBaseURL(baseURL) {
		baseURL = xai.DefaultCLIBaseURL
	}
	return xai.BuildBillingURLWithValidator(baseURL, weekly, validator)
}

func buildGrokMediaURL(account *Account, cfg *config.Config, endpoint GrokMediaEndpoint, requestID string) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	baseURL := account.GetGrokMediaBaseURL()
	switch endpoint {
	case GrokMediaEndpointImagesGenerations:
		return xai.BuildImagesGenerationsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointImagesEdits:
		return xai.BuildImagesEditsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointVideosGenerations:
		return xai.BuildVideosGenerationsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointVideosEdits:
		return xai.BuildVideosEditsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointVideosExtensions:
		return xai.BuildVideosExtensionsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointVideoStatus:
		return xai.BuildVideoURLWithValidator(baseURL, requestID, validator)
	case GrokMediaEndpointVideoContent:
		videoURL, err := xai.BuildVideoURLWithValidator(baseURL, requestID, validator)
		if err != nil {
			return "", err
		}
		return videoURL + "/content", nil
	default:
		return "", fmt.Errorf("unsupported grok media endpoint: %s", endpoint)
	}
}

// buildGrokVoiceURL 返回官方 xAI Voice API 端点。
// Voice HTTP（/tts、/stt、/custom-voices）与 WebSocket（/realtime）仅由 api.x.ai 提供，
// CLI 对话代理并未实现这些接口；当账号 base_url 指向 CLI 代理或为空时，回退到 DefaultBaseURL。
func buildGrokVoiceURL(account *Account, cfg *config.Config, endpoint string) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	base := ""
	if account != nil {
		base = account.GetGrokMediaBaseURL()
	}
	if strings.TrimSpace(base) == "" || isGrokCLIProxyBaseURL(base) {
		base = xai.DefaultBaseURL
	}
	validated, err := validator(base)
	if err != nil {
		return "", err
	}
	ep := strings.Trim(strings.TrimSpace(endpoint), "/")
	if ep == "" {
		return "", fmt.Errorf("voice endpoint is required")
	}
	parts := strings.Split(ep, "/")
	encoded := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid voice endpoint path")
		}
		encoded = append(encoded, url.PathEscape(part))
	}
	return strings.TrimRight(validated, "/") + "/" + strings.Join(encoded, "/"), nil
}

func isGrokCLIProxyBaseURL(raw string) bool {
	return isGrokCLIProxyTarget(raw)
}

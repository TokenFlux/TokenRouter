// 官方平台子端点选择保持原规则；目标安全策略由外层投影的 validator 执行。
package grok

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

func RedactedBaseURLValidator(validator BaseURLValidator) BaseURLValidator {
	return func(raw string) (string, error) {
		validated, err := validator(raw)
		if err != nil {
			return "", errors.New("base URL rejected by URL security policy")
		}
		return validated, nil
	}
}
func BuildBillingEndpointURL(baseURL string, weekly bool, validator BaseURLValidator) (string, error) {
	// 官方公共或区域 API 主机不提供 Grok Build 账单接口。
	// 自定义中继可能同时代理推理与 CLI 账单路径，因此继续使用其配置主机。
	if IsOfficialBaseURL(baseURL) && !(MediaCodec{}).IsGrokCLIProxyTarget(baseURL) {
		baseURL = DefaultCLIBaseURL
	}
	return BuildBillingURLWithValidator(baseURL, weekly, validator)
}
func BuildMediaEndpointURL(baseURL string, endpoint GrokMediaEndpoint, requestID string, validator BaseURLValidator) (string, error) {
	switch endpoint {
	case GrokMediaEndpointImagesGenerations:
		return BuildImagesGenerationsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointImagesEdits:
		return BuildImagesEditsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointVideosGenerations:
		return BuildVideosGenerationsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointVideosEdits:
		return BuildVideosEditsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointVideosExtensions:
		return BuildVideosExtensionsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointVideoStatus:
		return BuildVideoURLWithValidator(baseURL, requestID, validator)
	case GrokMediaEndpointVideoContent:
		videoURL, err := BuildVideoURLWithValidator(baseURL, requestID, validator)
		if err != nil {
			return "", err
		}
		return videoURL + "/content", nil
	default:
		return "", fmt.Errorf("unsupported grok media endpoint: %s", endpoint)
	}
}
func BuildVoiceEndpointURL(base string, endpoint string, validator BaseURLValidator) (string, error) {
	if strings.TrimSpace(base) == "" || (MediaCodec{}).IsGrokCLIProxyTarget(base) {
		base = DefaultBaseURL
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

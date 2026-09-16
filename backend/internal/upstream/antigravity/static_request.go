// 历史 upstream 类型的 Claude 直连保留双 Header 鉴权，与 OAuth 原生链分开。
package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
)

type StaticRequestInput struct {
	BaseURL, APIKey, Version, Beta string
	Sanitize                       func([]byte, string) ([]byte, bool)
}

func BuildStaticRequest(ctx context.Context, body []byte, input StaticRequestInput) (*http.Request, string, bool, error) {
	baseURL, apiKey := strings.TrimSpace(input.BaseURL), strings.TrimSpace(input.APIKey)
	if baseURL == "" || apiKey == "" {
		return nil, "", false, fmt.Errorf("upstream account missing base_url or api_key")
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	var request wire.ClaudeRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, "", false, fmt.Errorf("parse claude request: %w", err)
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, "", false, fmt.Errorf("missing model")
	}
	if sanitized, changed := input.Sanitize(body, input.Beta); changed {
		body = sanitized
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, "", false, fmt.Errorf("create upstream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("x-api-key", apiKey)
	if input.Version != "" {
		req.Header.Set("anthropic-version", input.Version)
	}
	if input.Beta != "" {
		req.Header.Set("anthropic-beta", input.Beta)
	}
	return req, request.Model, request.Stream, nil
}

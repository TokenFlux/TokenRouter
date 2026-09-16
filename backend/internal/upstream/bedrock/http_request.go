// 本文件构造一次 Bedrock 签名请求，不选择账号或读取配置。
package bedrock

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
)

// buildUpstreamRequestBedrock 构建 Bedrock 上游请求
func BuildRequest(
	ctx context.Context,
	body []byte,
	modelID string,
	region string,
	stream bool,
	signer *BedrockSigner,
) (*http.Request, error) {
	targetURL := BuildBedrockURL(region, modelID, stream)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// SigV4 签名
	if err := signer.SignRequest(ctx, req, body); err != nil {
		return nil, fmt.Errorf("sign bedrock request: %w", err)
	}

	return req, nil
}

// buildUpstreamRequestBedrockAPIKey 构建 Bedrock API Key (Bearer Token) 上游请求
func BuildRequestAPIKey(
	ctx context.Context,
	body []byte,
	modelID string,
	region string,
	stream bool,
	apiKey string,
) (*http.Request, error) {
	targetURL := BuildBedrockURL(region, modelID, stream)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	return req, nil
}

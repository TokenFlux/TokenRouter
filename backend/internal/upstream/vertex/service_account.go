// Vertex 迁移保留原协议及取消边界，旧入口仅投影。
package vertex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/TokenFlux/TokenRouter/internal/upstream/internal/googleauth"
)

const DefaultLocation = "us-central1"
const DefaultTokenURL = "https://oauth2.googleapis.com/token"
const AnthropicVersion = "vertex-2023-10-16"

var (
	vertexLocationPattern                = regexp.MustCompile(`^[a-z0-9-]+$`)
	vertexAnthropicDatedModelIDPattern   = regexp.MustCompile(`^(.+)-([0-9]{8})$`)
	vertexAnthropicAlreadyDatedIDPattern = regexp.MustCompile(`^.+@[0-9]{8}$`)
)

func ParseVertexServiceAccountJSON(raw []byte) (*google.ServiceAccountKey, error) {
	var key google.ServiceAccountKey
	if err := json.Unmarshal(raw, &key); err != nil {
		return nil, fmt.Errorf("invalid service account json: %w", err)
	}
	if strings.TrimSpace(key.ClientEmail) == "" {
		return nil, errors.New("service account json missing client_email")
	}
	if strings.TrimSpace(key.PrivateKey) == "" {
		return nil, errors.New("service account json missing private_key")
	}
	if strings.TrimSpace(key.ProjectID) == "" {
		return nil, errors.New("service account json missing project_id")
	}
	// 固定 Google token 端点，拒绝凭据中的自定义 token_uri。
	key.TokenURI = DefaultTokenURL
	return &key, nil
}

// ServiceAccountProjectID 复用完整凭据校验，供账号按需解析项目标识。
func ServiceAccountProjectID(raw []byte) (string, error) {
	key, err := ParseVertexServiceAccountJSON(raw)
	if err != nil {
		return "", err
	}
	return key.ProjectID, nil
}

func BuildVertexGeminiURL(projectID, location, model, action string, stream bool) (string, error) {
	projectID = strings.TrimSpace(projectID)
	location = strings.TrimSpace(location)
	model = strings.TrimSpace(model)
	action = strings.TrimSpace(action)
	if projectID == "" {
		return "", errors.New("vertex project_id is required")
	}
	if location == "" {
		location = DefaultLocation
	}
	if !vertexLocationPattern.MatchString(location) {
		return "", fmt.Errorf("invalid vertex location: %s", location)
	}
	if model == "" {
		return "", errors.New("vertex model is required")
	}
	switch action {
	case "generateContent", "streamGenerateContent", "countTokens":
	default:
		return "", fmt.Errorf("unsupported vertex gemini action: %s", action)
	}
	host := fmt.Sprintf("%s-aiplatform.googleapis.com", location)
	if location == "global" {
		host = "aiplatform.googleapis.com"
	}
	u := fmt.Sprintf(
		"https://%s/v1/projects/%s/locations/%s/publishers/google/models/%s:%s",
		host,
		url.PathEscape(projectID),
		url.PathEscape(location),
		url.PathEscape(model),
		action,
	)
	if stream {
		u += "?alt=sse"
	}
	return u, nil
}
func BuildVertexAnthropicURL(projectID, location, model string, stream bool) (string, error) {
	projectID = strings.TrimSpace(projectID)
	location = strings.TrimSpace(location)
	model = strings.TrimSpace(model)
	if projectID == "" {
		return "", errors.New("vertex project_id is required")
	}
	if location == "" {
		location = DefaultLocation
	}
	if !vertexLocationPattern.MatchString(location) {
		return "", fmt.Errorf("invalid vertex location: %s", location)
	}
	if model == "" {
		return "", errors.New("vertex model is required")
	}
	action := "rawPredict"
	if stream {
		action = "streamRawPredict"
	}
	host := fmt.Sprintf("%s-aiplatform.googleapis.com", location)
	if location == "global" {
		host = "aiplatform.googleapis.com"
	}
	escapedModel := strings.ReplaceAll(url.PathEscape(model), "%40", "@")
	return fmt.Sprintf(
		"https://%s/v1/projects/%s/locations/%s/publishers/anthropic/models/%s:%s",
		host,
		url.PathEscape(projectID),
		url.PathEscape(location),
		escapedModel,
		action,
	), nil
}
func NormalizeVertexAnthropicModelID(model string) string {
	model = strings.TrimSpace(model)
	if model == "" || vertexAnthropicAlreadyDatedIDPattern.MatchString(model) {
		return model
	}
	if m := vertexAnthropicDatedModelIDPattern.FindStringSubmatch(model); len(m) == 3 {
		return m[1] + "@" + m[2]
	}
	return model
}
func BuildVertexAnthropicRequestBody(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse anthropic vertex request body: %w", err)
	}
	delete(payload, "model")
	payload["anthropic_version"] = AnthropicVersion
	return json.Marshal(payload)
}

// ExchangeServiceAccountToken 只进行供应商签名和交换，缓存与锁归账号用例。
// @project-doc docs/interfaces/gemini_upstream.md#vertex_service_account_execution
func ExchangeServiceAccountToken(ctx context.Context, key *google.ServiceAccountKey, proxyURL string) (string, time.Duration, error) {
	return googleauth.ExchangeServiceAccountToken(ctx, key, proxyURL)
}

// NewServiceAccountHTTPClient 保留原独立传输参数与 timing 安装。
func NewServiceAccountHTTPClient(proxyURL string) (*http.Client, error) {
	return googleauth.NewServiceAccountHTTPClient(proxyURL)
}

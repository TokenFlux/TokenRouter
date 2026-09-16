// 本文件构造 Gemini 平台请求，凭据与目标策略只通过显式投影提供。
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

// CredentialMode 描述传输认证形式，不引入业务账号实体。
type CredentialMode string

const (
	APIKeyCredential         CredentialMode = "apikey"
	OAuthCredential          CredentialMode = "oauth"
	ServiceAccountCredential CredentialMode = "service_account"
)

type TokenSnapshot struct{ AccessToken, ProjectID string }
type RequestPlan struct {
	Mode                                                CredentialMode
	Model, Action                                       string
	Native, ForceAIStudio, ClientStream, UpstreamStream bool
	APIKey                                              func() string
	Token                                               func(context.Context) (TokenSnapshot, error)
	BaseURL                                             func() string
	ValidateURL                                         func(string) (string, error)
	VertexURL                                           func(string, bool) (string, error)
}

// BuildRequest 保留取 token 后读取 project 的次序，并区分原生 body 与兼容 REST 净化。
func BuildRequest(ctx context.Context, body []byte, plan RequestPlan) (*http.Request, string, error) {
	action, stream := plan.Action, plan.UpstreamStream
	if !plan.Native {
		stream = plan.ClientStream
		if plan.Mode == OAuthCredential {
			stream = plan.UpstreamStream
		}
		action = "generateContent"
		if stream {
			action = "streamGenerateContent"
		}
	}
	var apiKey string
	var token TokenSnapshot
	var err error
	switch plan.Mode {
	case APIKeyCredential:
		apiKey = plan.APIKey()
		if strings.TrimSpace(apiKey) == "" {
			return nil, "", errors.New("gemini api_key not configured")
		}
	case OAuthCredential, ServiceAccountCredential:
		if plan.Token == nil {
			return nil, "", errors.New("gemini token provider not configured")
		}
		token, err = plan.Token(ctx)
		if err != nil {
			return nil, "", err
		}
	default:
		return nil, "", fmt.Errorf("unsupported account type: %s", plan.Mode)
	}
	var target string
	wire := body
	wrapped := false
	if plan.Mode == ServiceAccountCredential {
		target, err = plan.VertexURL(action, stream)
	} else if plan.Mode == OAuthCredential && strings.TrimSpace(token.ProjectID) != "" && !plan.ForceAIStudio {
		base, validationErr := plan.ValidateURL(codeassist.GeminiCliBaseURL)
		if validationErr != nil {
			return nil, "", validationErr
		}
		target = fmt.Sprintf("%s/v1internal:%s", strings.TrimRight(base, "/"), action)
		if stream {
			target += "?alt=sse"
		}
		var inner any
		if err := json.Unmarshal(body, &inner); err != nil {
			return nil, "", fmt.Errorf("failed to parse gemini request: %w", err)
		}
		wire, _ = json.Marshal(map[string]any{"model": plan.Model, "project": strings.TrimSpace(token.ProjectID), "request": inner})
		wrapped = true
	} else {
		base, validationErr := plan.ValidateURL(plan.BaseURL())
		if validationErr != nil {
			return nil, "", validationErr
		}
		target, err = BuildGeminiAIStudioModelActionURL(base, plan.Model, action, stream)
	}
	if err != nil {
		return nil, "", err
	}
	if !plan.Native && !wrapped {
		wire = bridge.NativeNormalizeGeminiRequestForAIStudio(wire)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(wire))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if plan.Mode == APIKeyCredential {
		req.Header.Set("x-goog-api-key", apiKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	}
	if wrapped {
		req.Header.Set("User-Agent", codeassist.GeminiCLIUserAgent)
	}
	return req, "x-request-id", nil
}

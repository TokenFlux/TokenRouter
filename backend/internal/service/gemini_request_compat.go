// 请求构造只投影当前账号凭据、Vertex 端点与 egress 校验；平台不持有旧实体。
package service

import (
	"context"
	"errors"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
)

func (s *GeminiMessagesCompatService) geminiRequestPlan(value *gatewayprovider.ExecutionAccount, model, action string, native, clientStream, upstreamStream, forceAIStudio bool) gemininative.RequestPlan {
	plan := gemininative.RequestPlan{Mode: gemininative.CredentialMode(value.Record.Type), Model: model, Action: action, Native: native, ClientStream: clientStream, UpstreamStream: upstreamStream, ForceAIStudio: forceAIStudio, APIKey: func() string { return value.View().GetCredential("api_key") }, BaseURL: func() string { return value.View().GetGeminiBaseURL(geminicli.AIStudioBaseURL) }, ValidateURL: s.validateUpstreamBaseURL, VertexURL: func(action string, stream bool) (string, error) {
		return vertex.BuildVertexGeminiURL(gatewayprovider.ExecutionProtocolRecord(value).VertexProjectID(vertex.ServiceAccountProjectID), gatewayprovider.ExecutionProtocolRecord(value).VertexLocation(model), model, action, stream)
	}}
	plan.Token = func(ctx context.Context) (gemininative.TokenSnapshot, error) {
		if s.tokenProvider == nil {
			return gemininative.TokenSnapshot{}, errors.New("gemini token provider not configured")
		}
		token, err := accountToken(ctx, s.tokenProvider, value)
		if err != nil {
			return gemininative.TokenSnapshot{}, err
		}
		return gemininative.TokenSnapshot{AccessToken: token, ProjectID: value.View().GetCredential("project_id")}, nil
	}
	return plan
}

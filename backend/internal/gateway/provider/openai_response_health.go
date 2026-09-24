package provider

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// ApplyOpenAIResponseHealth 固化请求范围与模型观测，保留默认状态处理的字段写回边界。
func ApplyOpenAIResponseHealth(ctx context.Context, health *accountprovider.OpenAIResponseHealth, target *ExecutionAccount, status int, headers http.Header, body []byte, suppressDefaultRateLimit bool, models ...string) account.UpstreamErrorDecision {
	return health.Apply(ctx, target.View(), accountprovider.OpenAIResponseHealthInput{
		Observation:              HealthObservationFromContext(ctx, status, headers, body, models),
		Models:                   models,
		SuppressDefaultRateLimit: suppressDefaultRateLimit,
		ContentRejected:          IsContentPolicyRejection(body) || IsOpenAICyberWarningPayload(body, upstream.ExtractErrorMessage(body)),
		RequestScoped:            IsRequestScopedAccountFailure(target, status, body),
		SelfBuiltImage:           openai.IsOpenAIImagesSelfBuiltRequest(ctx),
		Transient:                IsTransientAccountFailure(status, body),
	})
}

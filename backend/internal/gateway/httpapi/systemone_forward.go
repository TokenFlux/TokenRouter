package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/systemone"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/jev"
)

// SystemOneExecutor 组合价格预检、提供商传输和单次 Jev 执行。
type SystemOneExecutor struct {
	Requests *OpenAIRequests
	Output   *OpenAIResponseOutput
	Pricing  *admission.ModelPricing
	Enter    func() (func(), error)
}

// Forward 在当前提供商上执行请求，返回交付失败时已观测到的用量。
func (s *SystemOneExecutor) Forward(ctx context.Context, c *gin.Context, target *gatewayprovider.ExecutionProvider, body []byte) (upstream.AttemptResult, error) {
	var result upstream.AttemptResult
	if target == nil || target.Record.Platform != provider.PlatformJev {
		return result, errors.New("jev provider is required")
	}
	selected, err := gatewayprovider.ProviderForProtocolAttempt(ctx, target)
	if err != nil {
		return result, err
	}
	request, err := systemone.ParseRequest(body)
	if err != nil {
		return result, err
	}
	mapped := gatewayprovider.ExecutionModelPolicy(selected).Mapped(request.Model)
	mapping := routing.GroupMappingResult{MappedModel: request.Model}
	requested := request.Model
	if plan, exists := requeststate.RoutePlanFromContext(ctx); exists {
		mapping = plan.Mapping()
		requested = plan.Models().RequestedModel
	}
	if s.Pricing != nil {
		key, _ := EffectiveAPIKey(c)
		var groupID *int64
		if key != nil {
			groupID = key.GroupID
		}
		billingModel := completion.OpenAIUsageBillingModel(&completion.Result{Model: request.Model, UpstreamModel: mapped}, mapping.ToUsageFields(requested, mapped))
		if err = s.Pricing.Check(ctx, groupID, billingModel); err != nil {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, 400, "invalid_request_error", "model_pricing_unavailable", admission.ModelPricingUnavailableMessage, false, false)
			return result, err
		}
	}
	base, err := s.Requests.ValidateBaseURL(selected.View().GetJevBaseURL())
	if err != nil {
		return result, err
	}
	body, err = systemone.ReplaceModel(body, mapped)
	if err != nil {
		return result, err
	}
	SetOpsUpstreamModel(c, mapped)
	SetActualUpstreamEndpoint(c, EndpointSystemOne)
	proxy := ""
	if selected.Record.Proxy != nil {
		proxy = selected.Record.Proxy.URL()
	}
	t := &jev.Target{
		ProviderID: selected.Record.ID, URL: jev.EndpointURL(base, "systemone"), Model: mapped,
		Token: selected.View().GetCredential("api_key"), ReadLimit: s.Output.Options.ReadLimit, Enter: s.Enter,
		ApplyHeaders: gatewayprovider.BindExecutionHeaders(selected),
		Do: func(req *http.Request) (*http.Response, error) {
			return s.Requests.Transport.Do(req, proxy, selected.Record.ID, selected.Record.Concurrency)
		},
		WriteHeaders: func(out, in http.Header) { egressprovider.WriteFilteredHeaders(out, in, s.Output.Headers) },
	}
	result, err = (jev.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocol.ProtocolSystemOne, Body: body, ResponseModel: requested, Target: t}, ResponseSink{Writer: c.Writer})
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, result.Duration.Milliseconds())
	if result.Served {
		return result, err
	}
	if failure, ok := errors.AsType[*jev.HTTPError](err); ok {
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{Platform: provider.PlatformJev, ProviderID: selected.Record.ID, ProviderName: selected.Record.Name, UpstreamStatusCode: failure.Status, UpstreamRequestID: failure.Header.Get("x-request-id"), Kind: "upstream_error", Message: failure.Error()})
		// 请求校验错误由上游报文解释，提供商健康不受此类输入影响。
		if failure.Status == http.StatusUnprocessableEntity {
			WriteEmbeddingsUpstreamResponse(c, &http.Response{StatusCode: failure.Status, Header: failure.Header}, failure.Body, s.Output.Headers)
			return result, err
		}
		decision := gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, selected, failure.Status, failure.Header, failure.Body, false, mapped)
		retry := failure.Status == 401 || failure.Status == 403 || failure.Status == 429 || failure.Status >= 500
		if decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(selected), failure.Status, retry) && !decision.ShouldReturnGenericError() {
			return result, &forward.UpstreamFailoverError{StatusCode: failure.Status, ResponseBody: failure.Body, ResponseHeaders: failure.Header.Clone()}
		}
		if decision.ShouldReturnGenericError() {
			return result, err
		}
		WriteEmbeddingsUpstreamResponse(c, &http.Response{StatusCode: failure.Status, Header: failure.Header}, failure.Body, s.Output.Headers)
		return result, err
	}
	if result.FailureClass == "transport" && ctx.Err() == nil {
		return result, &forward.UpstreamFailoverError{StatusCode: http.StatusBadGateway}
	}
	return result, err
}

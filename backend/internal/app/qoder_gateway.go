package app

import (
	"log/slog"
	"time"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"

	"github.com/TokenFlux/TokenRouter/internal/server/clientip"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// provideQoderChat 在组合根一次绑定依赖；HTTP 入口不再组装业务回调。
func provideQoderChat(g *service.GatewayService, q *gatewayprovider.QoderRuntime, refresh *accountprovider.QoderRequestRefresh, c *scheduler.ConcurrencyService, b *admission.FundingAdmission, k *apikey.APIKeyService, r *errorpolicy.ErrorPassthroughService, pool *completion.UsageRecordWorkerPool, recorders GatewayCompletionRecorders, activity *qoderRequestActivity, requests *gatewayRequestActivity, choices *selection.Generic) *gatewayhttp.QoderChatHandler {
	runtime := &qoderRuntime{Gateway: g, Choices: choices, Qoder: q, Refresh: refresh, Billing: b, Keys: k, Completions: pool, Recorder: recorders.Forward}
	useCase := gateway.NewQoderExecutor(3, 30*time.Second, c, runtime)
	useCase.Enter = activity.Enter
	var matcher gatewayhttp.ErrorRuleMatcher
	if r != nil {
		matcher = r
	}
	presenter := gatewayhttp.QoderErrorPresenter{Rules: matcher, Describe: gatewayprovider.DescribeQoderError, ReadAccess: keyhttp.GetAPIKeyFromContext, Catalogue: gatewayprovider.ModelDisplayCatalogue{}}
	result := &gatewayhttp.QoderChatHandler{Executor: useCase, Failure: presenter.Failure}
	result.BindRequestActivity(requests.Enter)
	result.Preflight = qoderPreflight
	result.PrepareRequest = func(c *gin.Context, parsed gatewayhttp.ParsedRequest) (gateway.Request, error) {
		if err := qoderPreflight(c); err != nil {
			return gateway.Request{}, err
		}
		key, _ := keyhttp.GetAPIKeyFromContext(c)
		subject, _ := authctx.GetAuthSubjectFromContext(c)
		subscription, _ := gatewayhttp.SubscriptionFromContext(c)
		if r != nil {
			gatewayhttp.BindErrorPassthroughService(c, r)
		}
		gatewayhttp.SetOpsRequestContext(c, parsed.Model, parsed.Stream)
		gatewayhttp.SetOpsEndpointContext(c, "", int16(usage.RequestTypeFromLegacy(parsed.Stream, false)))
		var access *apikey.AccessSnapshot
		if value, ok := c.Get("apikey_access_snapshot"); ok {
			access, _ = value.(*apikey.AccessSnapshot)
		}
		keyView, ok := gatewayhttp.EffectiveAPIKey(c)
		if !ok {
			keyView = apikey.CopyAPIKey(key)
		}
		inbound, outbound := gatewayhttp.GetInboundEndpoint(c), gatewayhttp.GetUpstreamEndpoint(c, capability.PlatformQoder)
		request := gateway.Request{
			Access: access, UserID: subject.UserID, Concurrency: subject.Concurrency, Stream: parsed.Stream,
			Body: append([]byte(nil), parsed.Body...), Model: parsed.Model,
			Funding:  gateway.FundingState{Key: keyView, Subscription: subscription},
			Metadata: gateway.RequestMetadata{Headers: c.Request.Header.Clone(), UserAgent: c.GetHeader("User-Agent"), ClientIP: clientip.GetClientIP(c), InboundEndpoint: inbound, UpstreamEndpoint: outbound, QuotaPlatform: admission.QuotaPlatform(c.Request.Context(), key), ClaudeCode: requeststate.IsClaudeCodeClient(c.Request.Context()), StartedAt: parsed.StartedAt},
		}
		request.SessionHash = session.QoderRequestHash(request.Metadata.Headers, request.Body, "anthropic", &requeststate.SessionContext{ClientIP: request.Metadata.ClientIP, UserAgent: request.Metadata.UserAgent, APIKeyID: key.ID}, slog.Info)
		return request, nil
	}
	result.Observer = func(c *gin.Context, request gateway.Request) gateway.ExecutionObserver {
		return &qoderHTTPObservation{c: c, stream: request.Stream}
	}
	return result
}

// qoderPreflight 保持读取请求体前的鉴权错误顺序。
func qoderPreflight(c *gin.Context) error {
	if _, ok := keyhttp.GetAPIKeyFromContext(c); !ok {
		return &gatewayhttp.HTTPFailure{Status: 401, Type: "authentication_error", Message: "Invalid API key"}
	}
	if _, ok := authctx.GetAuthSubjectFromContext(c); !ok {
		return &gatewayhttp.HTTPFailure{Status: 500, Type: "api_error", Message: "User context not found"}
	}
	return nil
}

// qoderHTTPObservation 只同步接收观测，不能被完成队列捕获。
type qoderHTTPObservation struct {
	c               *gin.Context
	stream, started bool
}

func (o *qoderHTTPObservation) Prepared(request gateway.Request) {
	o.c.Request = o.c.Request.WithContext(requeststate.WithRoutePlan(o.c.Request.Context(), request.Route))
	gatewayhttp.SetOpsLatencyMs(o.c, gatewayhttp.OpsAuthLatencyMsKey, time.Since(request.Metadata.StartedAt).Milliseconds())
}
func (o *qoderHTTPObservation) Selected(snapshot account.AccountSnapshot) {
	gatewayhttp.SetOpsSelectedAccount(o.c, snapshot.ID, snapshot.Platform)
}
func (o *qoderHTTPObservation) Waiting(string) scheduler.WaitObserver {
	return gatewayhttp.WaitObserver(o.c, gatewayhttp.SSEPingFormatComment, 10*time.Second, o.stream, &o.started, true)
}

// qoderRequestActivity 让 Chat、兼容入口与平台执行共享一个现有停止拥有者。
type qoderRequestActivity struct{ *lifecycle.Operations }

func provideQoderRequestActivity(manager *lifecycle.Manager, runtime *gatewayprovider.QoderRuntime) *qoderRequestActivity {
	activity := lifecycle.NewOperations("QoderRequestsAndAttempts")
	manager.Register(lifecycle.Hook{Name: "QoderRequestsAndAttempts", StopOrder: 15, Stop: activity.StopContext})
	runtime.BindAttemptActivity(activity.Enter)
	return &qoderRequestActivity{Operations: activity}
}

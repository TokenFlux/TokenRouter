package app

import (
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// provideQoderChat 在组合根一次绑定依赖；HTTP 入口不再组装业务回调。
func provideQoderChat(g *service.GatewayService, q *service.QoderGatewayService, c *service.ConcurrencyService, b *service.BillingCacheService, k *service.APIKeyService, h *handler.QoderGatewayHandler, r *service.ErrorPassthroughService, pool *completion.UsageRecordWorkerPool, recorders GatewayCompletionRecorders, manager *lifecycle.Manager, requests *gatewayRequestActivity) *gatewayhttp.QoderChatHandler {
	activity := lifecycle.NewOperations("QoderRequestsAndAttempts")
	manager.Register(lifecycle.Hook{Name: "QoderRequestsAndAttempts", StopOrder: 15, Stop: activity.StopContext})
	q.BindAttemptActivity(activity.Enter)
	h.BindRequestActivity(activity.Enter)
	runtime := &qoderRuntime{Gateway: g, Qoder: q, Billing: b, Keys: k, Completions: pool, Recorder: recorders.Forward}
	useCase := gateway.NewQoderExecutor(3, 30*time.Second, c, runtime)
	useCase.Enter = activity.Enter
	result := &gatewayhttp.QoderChatHandler{Executor: useCase, Failure: h.QoderClientFailure}
	result.BindRequestActivity(requests.Enter)
	result.Preflight = qoderPreflight
	result.PrepareRequest = func(c *gin.Context, parsed gatewayhttp.ParsedRequest) (gateway.Request, error) {
		if err := qoderPreflight(c); err != nil {
			return gateway.Request{}, err
		}
		key, _ := middleware.GetAPIKeyFromContext(c)
		subject, _ := middleware.GetAuthSubjectFromContext(c)
		subscription, _ := middleware.GetSubscriptionFromContext(c)
		if r != nil {
			service.BindErrorPassthroughService(c, r)
		}
		handler.ObserveQoderRequest(c, parsed.Model, parsed.Stream)
		var access *apikey.AccessSnapshot
		if value, ok := c.Get("apikey_access_snapshot"); ok {
			access, _ = value.(*apikey.AccessSnapshot)
		}
		keyView, ok := gatewayhttp.EffectiveAPIKey(c)
		if !ok {
			keyView = service.APIKeyView(key)
		}
		inbound, outbound := handler.QoderEndpoints(c, service.PlatformQoder)
		request := gateway.Request{
			Access: access, UserID: subject.UserID, Concurrency: subject.Concurrency, Stream: parsed.Stream,
			Body: append([]byte(nil), parsed.Body...), Model: parsed.Model,
			Funding:  gateway.FundingState{Key: keyView, Subscription: subscription},
			Metadata: gateway.RequestMetadata{Headers: c.Request.Header.Clone(), UserAgent: c.GetHeader("User-Agent"), ClientIP: ip.GetClientIP(c), InboundEndpoint: inbound, UpstreamEndpoint: outbound, QuotaPlatform: service.QuotaPlatform(c.Request.Context(), key), ClaudeCode: service.IsClaudeCodeClient(c.Request.Context()), StartedAt: parsed.StartedAt},
		}
		request.SessionHash = session.QoderRequestHash(request.Metadata.Headers, request.Body, "anthropic", &requeststate.SessionContext{ClientIP: request.Metadata.ClientIP, UserAgent: request.Metadata.UserAgent, APIKeyID: key.ID}, slog.Info)
		return request, nil
	}
	result.Observer = func(c *gin.Context, request gateway.Request) gateway.ExecutionObserver {
		return &qoderHTTPObservation{c: c, stream: request.Stream}
	}
	h.BindChatHandler(result)
	return result
}

// qoderPreflight 保持读取请求体前的鉴权错误顺序。
func qoderPreflight(c *gin.Context) error {
	if _, ok := middleware.GetAPIKeyFromContext(c); !ok {
		return &gatewayhttp.HTTPFailure{Status: 401, Type: "authentication_error", Message: "Invalid API key"}
	}
	if _, ok := middleware.GetAuthSubjectFromContext(c); !ok {
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
	o.c.Request = o.c.Request.WithContext(service.WithRoutePlan(o.c.Request.Context(), request.Route))
	service.SetOpsLatencyMs(o.c, service.OpsAuthLatencyMsKey, time.Since(request.Metadata.StartedAt).Milliseconds())
}
func (o *qoderHTTPObservation) Selected(snapshot account.AccountSnapshot) {
	handler.ObserveQoderSelection(o.c, snapshot.ID, snapshot.Platform)
}
func (o *qoderHTTPObservation) Waiting(string) scheduler.WaitObserver {
	return gatewayhttp.WaitObserver(o.c, gatewayhttp.SSEPingFormatComment, 10*time.Second, o.stream, &o.started, true)
}

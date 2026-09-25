package httpapi

import (
	"context"
	"errors"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	"github.com/gin-gonic/gin"
)

type openAIWSSessionPreemptKey struct {
	groupID     int64
	apiKeyID    int64
	sessionHash string
}

type openAIWSSessionPreemptContextKey struct{}

// BeginOpenAIWSIngressSessionPreemption keeps a persistent inbound WS session
// registered across upstream retry attempts. Nested forwarding calls reuse the
// registration so returning from one attempt cannot create a preemption gap.
func (s *OpenAIWebSocketExecutor) BeginOpenAIWSIngressSessionPreemption(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	firstClientMessage []byte,
) (context.Context, func(), bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if armed, _ := ctx.Value(openAIWSSessionPreemptContextKey{}).(bool); armed {
		return ctx, func() {}, true
	}
	if s != nil && s.Options != nil && s.Options.ModeRouterV2Enabled &&
		account != nil && account.View().ResolveOpenAIResponsesWebSocketV2Mode(s.Options.IngressModeDefault) == accountcore.OpenAIWSIngressModePassthrough {
		return ctx, func() {}, false
	}

	preemptSessionHash := ""
	preemptGroupID := OpenAIResponseGroupID(c)
	if account != nil && account.Record.Platform == capability.PlatformOpenAI && account.Record.Type == capability.AccountTypeOAuth {
		preemptSessionHash = GenerateOpenAISessionHash(c, firstClientMessage)
	}
	preemptCtx, cleanup, armed, preemptedPrevious := s.beginOpenAIWSSessionPreemptContext(
		ctx,
		account,
		preemptGroupID, APIKeyIDFromContext(c), preemptSessionHash,
		false,
	)
	if !armed {
		return ctx, func() {}, false
	}
	if preemptedPrevious {
		if stateStore := s.State; stateStore != nil {
			stateStore.DeleteSessionTurnState(preemptGroupID, preemptSessionHash)
			stateStore.DeleteSessionConn(preemptGroupID, preemptSessionHash)
		}
	}
	return context.WithValue(preemptCtx, openAIWSSessionPreemptContextKey{}, true), cleanup, true
}

func newOpenAIWSSessionPreemptKey(groupID, apiKeyID int64, sessionHash string) (openAIWSSessionPreemptKey, bool) {
	sessionHash = strings.TrimSpace(sessionHash)
	if groupID <= 0 || apiKeyID <= 0 || sessionHash == "" {
		return openAIWSSessionPreemptKey{}, false
	}
	return openAIWSSessionPreemptKey{groupID: groupID, apiKeyID: apiKeyID, sessionHash: sessionHash}, true
}

// 旧注册表保留零值构造能力；唯一所有者代次与取消表位于 gateway/ws。
type openAIWSSessionPreemptRegistry struct{ gatewayws.PreemptRegistry }

func (r *openAIWSSessionPreemptRegistry) Begin(key openAIWSSessionPreemptKey, cancel func()) (func(), bool) {
	if r == nil {
		return func() {}, false
	}
	return r.PreemptRegistry.Begin(gatewayws.PreemptKey{GroupID: key.groupID, APIKeyID: key.apiKeyID, SessionHash: key.sessionHash}, cancel)
}

func (s *OpenAIWebSocketExecutor) beginOpenAIWSSessionPreemptContext(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	groupID, apiKeyID int64,
	sessionHash string,
	httpIngressWSOneShot bool,
) (context.Context, func(), bool, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || account == nil || account.Record.Platform != capability.PlatformOpenAI || account.Record.Type != capability.AccountTypeOAuth || httpIngressWSOneShot {
		return ctx, func() {}, false, false
	}
	key, ok := newOpenAIWSSessionPreemptKey(groupID, apiKeyID, sessionHash)
	if !ok {
		return ctx, func() {}, false, false
	}

	return s.wsPreemption().Begin(ctx, gatewayws.PreemptKey{GroupID: key.groupID, APIKeyID: key.apiKeyID, SessionHash: key.sessionHash})
}

func (s *OpenAIWebSocketExecutor) openAIWSSessionPreemptionCache() session.OpenAIWSSessionPreemptionCache {
	if s == nil || s.Cache == nil {
		return nil
	}
	cache, _ := s.Cache.(session.OpenAIWSSessionPreemptionCache)
	return cache
}

// wsPreemption 只投影既有应用实例中的会话依赖。
func (s *OpenAIWebSocketExecutor) wsPreemption() *gatewayws.Preemption {
	return &gatewayws.Preemption{Registry: &s.openaiWSSessionPreemptions.PreemptRegistry, Cache: s.openAIWSSessionPreemptionCache(), State: s.State, RedisTimeout: session.StateStoreRedisTimeout}
}

func isOpenAIWSSessionPreempted(ctx context.Context) bool {
	return ctx != nil && errors.Is(context.Cause(ctx), gatewayws.ErrSessionPreempted)
}

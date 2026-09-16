package service

import (
	"context"
	"errors"
	"strings"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	"github.com/gin-gonic/gin"
)

var errOpenAIWSSessionPreempted = gatewayws.ErrSessionPreempted

type OpenAIWSSessionPreemptionCache = session.OpenAIWSSessionPreemptionCache

func NewOpenAIWSSessionPreemptedError() error {
	return errOpenAIWSSessionPreempted
}

type openAIWSSessionPreemptKey struct {
	groupID     int64
	apiKeyID    int64
	sessionHash string
}

type openAIWSSessionPreemptContextKey struct{}

// BeginOpenAIWSIngressSessionPreemption keeps a persistent inbound WS session
// registered across upstream retry attempts. Nested forwarding calls reuse the
// registration so returning from one attempt cannot create a preemption gap.
func (s *OpenAIGatewayService) BeginOpenAIWSIngressSessionPreemption(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	firstClientMessage []byte,
) (context.Context, func(), bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if armed, _ := ctx.Value(openAIWSSessionPreemptContextKey{}).(bool); armed {
		return ctx, func() {}, true
	}
	if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled &&
		account != nil && account.ResolveOpenAIResponsesWebSocketV2Mode(s.cfg.Gateway.OpenAIWS.IngressModeDefault) == OpenAIWSIngressModePassthrough {
		return ctx, func() {}, false
	}

	preemptSessionHash := ""
	preemptGroupID := getOpenAIGroupIDFromContext(c)
	if account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeOAuth {
		preemptSessionHash = s.GenerateSessionHash(c, firstClientMessage)
	}
	preemptCtx, cleanup, armed, preemptedPrevious := s.beginOpenAIWSSessionPreemptContext(
		ctx,
		account,
		preemptGroupID,
		getAPIKeyIDFromContext(c),
		preemptSessionHash,
		false,
	)
	if !armed {
		return ctx, func() {}, false
	}
	if preemptedPrevious {
		if stateStore := s.getOpenAIWSStateStore(); stateStore != nil {
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

func (s *OpenAIGatewayService) beginOpenAIWSSessionPreemptContext(
	ctx context.Context,
	account *Account,
	groupID, apiKeyID int64,
	sessionHash string,
	httpIngressWSOneShot bool,
) (context.Context, func(), bool, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || httpIngressWSOneShot {
		return ctx, func() {}, false, false
	}
	key, ok := newOpenAIWSSessionPreemptKey(groupID, apiKeyID, sessionHash)
	if !ok {
		return ctx, func() {}, false, false
	}

	return s.wsPreemption().Begin(ctx, gatewayws.PreemptKey{GroupID: key.groupID, APIKeyID: key.apiKeyID, SessionHash: key.sessionHash})
}

func (s *OpenAIGatewayService) openAIWSSessionPreemptionCache() OpenAIWSSessionPreemptionCache {
	if s == nil || s.cache == nil {
		return nil
	}
	cache, _ := s.cache.(OpenAIWSSessionPreemptionCache)
	return cache
}

// wsPreemption 只投影既有应用实例中的会话依赖。
func (s *OpenAIGatewayService) wsPreemption() *gatewayws.Preemption {
	return &gatewayws.Preemption{Registry: &s.openaiWSSessionPreemptions.PreemptRegistry, Cache: s.openAIWSSessionPreemptionCache(), State: s.getOpenAIWSStateStore(), RedisTimeout: openAIWSStateStoreRedisTimeout}
}

func isOpenAIWSSessionPreempted(ctx context.Context) bool {
	return ctx != nil && errors.Is(context.Cause(ctx), errOpenAIWSSessionPreempted)
}

// IsOpenAIWSSessionPreemptedError 委托唯一的会话抢占错误判断。
func IsOpenAIWSSessionPreemptedError(err error) bool { return gatewayws.IsSessionPreemptedError(err) }

package httpapi

import (
	"strings"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	rawwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/gin-gonic/gin"
)

const (
	upstreamResponseModelObserverContextKey = "upstream_response_model_observer"
)

func BeginUpstreamResponseModelObservation(c *gin.Context) *forwardcore.ResponseObserver {
	observer := &forwardcore.ResponseObserver{}
	if c != nil {
		c.Set(upstreamResponseModelObserverContextKey, observer)
	}
	return observer
}

func UpstreamResponseModelObserverFromContext(c *gin.Context) *forwardcore.ResponseObserver {
	if c == nil {
		return nil
	}
	value, ok := c.Get(upstreamResponseModelObserverContextKey)
	if !ok {
		return nil
	}
	observer, _ := value.(*forwardcore.ResponseObserver)
	return observer
}

func ObservedUpstreamResponseServiceTier(c *gin.Context) string {
	return UpstreamResponseModelObserverFromContext(c).ServiceTier()
}

// ResolvedOpenAIUpstreamServiceTierFromObserver 保留最终出站档位；实际响应
// 档位在用量记录时结合凭据协议处理，不在观测阶段提升请求档位。
func ResolvedOpenAIUpstreamServiceTierFromObserver(_ *forwardcore.ResponseObserver, outboundBodyTier *string) *string {
	return outboundBodyTier
}
func ResolvedOpenAIUpstreamServiceTier(c *gin.Context, outboundBodyTier *string) *string {
	return ResolvedOpenAIUpstreamServiceTierFromObserver(UpstreamResponseModelObserverFromContext(c), outboundBodyTier)
}

// ObserveOpenAIServiceTierInContext 将原始 OpenAI 响应事件写入当前请求的
// observer；模型审计字段仍保持 fork 既有关闭状态。
func ObserveOpenAIServiceTierInContext(c *gin.Context, payload []byte, eventType string) {
	if c == nil || len(payload) == 0 {
		return
	}
	observer := UpstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = BeginUpstreamResponseModelObservation(c)
	}
	observer.ObserveOpenAI(payload, eventType)
}

// ObserveOpenAISSEBody 逐帧记录 Responses SSE 中的实际服务档位。
func ObserveOpenAISSEBody(c *gin.Context, body string) {
	if c == nil || strings.TrimSpace(body) == "" {
		return
	}
	rawwire.ForEachOpenAISSEFrame(body, func(eventType string, payload []byte) {
		ObserveOpenAIServiceTierInContext(c, payload, eventType)
	})
}

package service

import (
	"errors"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway/compact"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
)

// openAICompactFallbackSignal 保留旧 errors.As 及私有字段访问；只投影原生失败值。
type openAICompactFallbackSignal struct {
	payload []byte
	message string
}

func (e *openAICompactFallbackSignal) native() *compact.Failure {
	if e == nil {
		return nil
	}
	return &compact.Failure{Payload: e.payload, Message: e.message}
}
func (e *openAICompactFallbackSignal) Error() string { return e.native().Error() }
func asOpenAICompactFallbackSignal(err error) (*openAICompactFallbackSignal, bool) {
	var signal *openAICompactFallbackSignal
	return signal, errors.As(err, &signal) && signal != nil
}

func isExplicitOpenAICompactContext(c *gin.Context) bool {
	return gatewayhttp.IsOpenAIResponsesCompactPath(c) || gatewayhttp.IsOpenAINativeCompactionV2(c)
}
func isExplicitOpenAICompactRequest(c *gin.Context, body []byte) bool {
	return gatewayhttp.IsOpenAIResponsesCompactPath(c) || protocolopenai.HasCompactionTriggerInInput(body)
}
func newOpenAICompactFallbackSignal(c *gin.Context, payload []byte, message string) error {
	signal := compactRecovery(nil, nil).NewFailure(isExplicitOpenAICompactContext(c), payload, message)
	if signal == nil {
		return nil
	}
	return &openAICompactFallbackSignal{payload: signal.Payload, message: signal.Message}
}

// 旧调用签名仅负责当前请求/账号投影；恢复规则由 gateway/compact 唯一实现。
func (s *OpenAIGatewayService) resolveOpenAICompactFallbackModel(account *gatewayprovider.ExecutionAccount, model string) string {
	return compactRecovery(s, account).ResolveModel(model)
}
func isOpenAICompactModelFailure(status int, message string, body []byte) bool {
	return compactRecovery(nil, nil).ModelFailure(status, message, body)
}

func openAICompactFallbackErrorResponse(resp *http.Response, signal *openAICompactFallbackSignal) (*http.Response, []byte) {
	return gatewayhttp.CompactFallbackErrorResponse(resp, signal.native())
}

func (s *OpenAIGatewayService) appendOpenAICompactFallbackRetryOps(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, payload []byte, message string, passthrough bool) {
	p := compactRetryAdapter{s: s, c: c, account: account, response: resp}
	p.observe(payload, message, passthrough)
}
func (s *OpenAIGatewayService) prepareOpenAICompactFallbackRetry(c *gin.Context, account *gatewayprovider.ExecutionAccount, requested string, body []byte, status int, message string, payload []byte, tried bool) ([]byte, string, bool) {
	in := compact.Request{Explicit: isExplicitOpenAICompactRequest(c, body), AlreadyRetried: tried, RequestedModel: requested, Body: body}
	return compactRecovery(s, account).Prepare(in, status, message, payload)
}
func (s *OpenAIGatewayService) applyOpenAIPassthroughCompactFallbackFromSignal(c *gin.Context, account *gatewayprovider.ExecutionAccount, requested string, body []byte, err error, tried bool, resp *http.Response) ([]byte, string, bool) {
	signal, ok := asOpenAICompactFallbackSignal(err)
	if !ok {
		return body, "", false
	}
	in := compact.Request{Explicit: isExplicitOpenAICompactRequest(c, body), AlreadyRetried: tried, RequestedModel: requested, Body: body}
	return compactRecovery(s, account).ApplySignal(in, signal.native(), &compactRetryAdapter{s: s, c: c, account: account, response: resp})
}

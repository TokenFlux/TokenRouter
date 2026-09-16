// Raw Chat 的账号策略、请求状态和旧缓存仅通过窄端口接入原生响应处理。
package service

import (
	"crypto/rand"
	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) nativeRawResponseOptions(c *gin.Context, resp *http.Response, account *Account, billingModel, upstreamModel string, serviceTier *string, writeError compatErrorWriter) native.RawResponseOptions {
	options := native.RawResponseOptions{
		Runtime:        bridge.Runtime{Now: time.Now, ReadRandom: rand.Read},
		Scanner:        s.newUpstreamSSEScanner,
		CC:             func() native.CCResponseOptions { return s.nativeCCResponseOptions(c, writeError) },
		ReadBody:       func(r io.Reader) ([]byte, error) { return ReadUpstreamResponseBody(r, s.cfg, c, openAITooLargeError) },
		BodyLimitError: ErrUpstreamResponseBodyTooLarge,
		Headers: func(dst, src http.Header) {
			if s.responseHeaderFilter != nil {
				responseheaders.WriteFilteredHeaders(dst, src, s.responseHeaderFilter)
			}
		},
		WriteError:    func(status int, kind, message string) { writeError(c, status, kind, message) },
		Observe:       func(body []byte, event string) { observeOpenAIServiceTierInContext(c, body, event) },
		ObserveSSE:    func(body string) { observeOpenAISSEBody(c, body) },
		TransformLine: func(line string) string { return applyOllamaCloudRawChatCompletionsSSELine(account, line) },
		TransformBody: func(body []byte) []byte { return applyOllamaCloudRawChatCompletionsResponse(account, body) },
		MissingUsage: func(model string, usage wire.ForwardUsage) error {
			if requiresBillableGrokChatUsage(account, billingModel, upstreamModel, model) && !hasBillableGrokChatUsage(usage) {
				return newGrokMissingUsageFailoverError(c, account, firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")))
			}
			return nil
		},
		ServiceTier:          func() string { return observedUpstreamResponseServiceTier(c) },
		ResolvedServiceTier:  func() *string { return resolvedOpenAIUpstreamServiceTier(c, serviceTier) },
		NormalizeServiceTier: normalizeObservedOpenAIServiceTier,
		TruncatedFailover: func(err error) error {
			return newOpenAIRawStreamTruncatedFailoverError(c, account, resp.Header.Get("x-request-id"), err)
		},
		RecordTruncation: func(err error) {
			recordOpenAIRawStreamTruncation(c, account, resp.Header.Get("x-request-id"), err, "http_error")
		},
		SilentRefusal: func() error { return newOpenAISilentRefusalFailoverError(c, account, resp.Header.Get("x-request-id")) },
		CacheOutput:   s.cacheReasoningItemsFromOutput,
		CacheEvents:   s.cacheReasoningItemsFromEvents,
	}
	if account != nil {
		options.AccountID = account.ID
	}
	return options
}

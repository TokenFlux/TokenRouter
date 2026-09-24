package httpapi

import (
	"crypto/rand"
	"io"
	"net/http"
	"time"

	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (p *OpenAIResponseOutput) RawOptions(c *gin.Context, resp *http.Response, account *gatewayprovider.ExecutionAccount, billingModel, upstreamModel string, serviceTier *string, writeError func(*gin.Context, int, string, string)) openai.RawResponseOptions {
	options := openai.RawResponseOptions{
		Runtime: bridge.Runtime{Now: time.Now, ReadRandom: rand.Read},
		Scanner: p.Scanner,
		CC:      func() openai.CCResponseOptions { return p.CCOptions(c, writeError) },
		ReadBody: func(r io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(r, p.Options.ReadLimit, c, OpenAIResponseTooLarge)
		},
		BodyLimitError: httpclient.ErrResponseBodyTooLarge,
		Headers: func(dst, src http.Header) {
			if p.Headers != nil {
				provider.WriteFilteredHeaders(dst, src, p.Headers)
			}
		},
		WriteError: func(status int, kind, message string) { writeError(c, status, kind, message) },
		Observe:    func(body []byte, event string) { ObserveOpenAIServiceTierInContext(c, body, event) },
		ObserveSSE: func(body string) { ObserveOpenAISSEBody(c, body) },
		TransformLine: func(line string) string {
			return gatewayprovider.ApplyOllamaCloudRawChatCompletionsSSELine(account, line)
		},
		TransformBody: func(body []byte) []byte {
			return gatewayprovider.ApplyOllamaCloudRawChatCompletionsResponse(account, body)
		},
		MissingUsage: func(model string, usage wire.ForwardUsage) error {
			if gatewayprovider.RequiresBillableGrokChatUsage(account, billingModel, upstreamModel, model) && !gatewayprovider.HasBillableGrokChatUsage(usage) {
				return NewGrokMissingUsageFailure(c, account, requeststate.FirstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")))
			}
			return nil
		},
		ServiceTier:          func() string { return ObservedUpstreamResponseServiceTier(c) },
		ResolvedServiceTier:  func() *string { return ResolvedOpenAIUpstreamServiceTier(c, serviceTier) },
		NormalizeServiceTier: forwardcore.NormalizeObservedOpenAIServiceTier,
		TruncatedFailover: func(err error) error {
			return NewOpenAIRawTruncationFailure(c, account, resp.Header.Get("x-request-id"), err)
		},
		RecordTruncation: func(err error) {
			RecordOpenAIRawTruncation(c, account, resp.Header.Get("x-request-id"), err, "http_error")
		},
		SilentRefusal: func() error {
			return NewOpenAISilentRefusalFailoverError(c, ExecutionErrorAccount(account), resp.Header.Get("x-request-id"))
		},
		CacheOutput: p.Reasoning.FromOutput,
		CacheEvents: p.Reasoning.FromEvents,
	}
	if account != nil {
		options.AccountID = account.Record.ID
	}
	return options
}

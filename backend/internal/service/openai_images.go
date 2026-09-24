package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	mediaprovider "github.com/TokenFlux/TokenRouter/internal/gateway/media/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/protocol"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/upstream"

	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	openAIImagesGenerationsURL = "https://api.openai.com/v1/images/generations"
	openAIImagesEditsURL       = "https://api.openai.com/v1/images/edits"
)

const (
	OpenAIImagesCapabilityBasic  accountcore.OpenAIImagesCapability = "images-basic"
	OpenAIImagesCapabilityNative accountcore.OpenAIImagesCapability = "images-native"
)

// ParseOpenAIImagesRequest 解析请求并按请求中的模型执行严格校验，供不涉及渠道映射的调用方使用。
func (s *OpenAIGatewayService) ParseOpenAIImagesRequest(c *gin.Context, body []byte) (*gatewaymedia.ImageRequest, error) {
	return s.parseOpenAIImagesRequest(c, body, true)
}

// ParseOpenAIImagesRequestForRouting 解析请求结构，但把模型族校验延后到渠道映射完成之后。
func (s *OpenAIGatewayService) ParseOpenAIImagesRequestForRouting(c *gin.Context, body []byte) (*gatewaymedia.ImageRequest, error) {
	return s.parseOpenAIImagesRequest(c, body, false)
}

func (s *OpenAIGatewayService) parseOpenAIImagesRequest(c *gin.Context, body []byte, validateModel bool) (*gatewaymedia.ImageRequest, error) {
	if c == nil || c.Request == nil {
		return nil, fmt.Errorf("missing request context")
	}
	return gatewaymedia.ParseImageRequest(c.Request.URL.Path, c.GetHeader("Content-Type"), body, validateModel)
}

func (s *OpenAIGatewayService) ForwardImages(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	parsed *gatewaymedia.ImageRequest,
	channelMappedModel string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed images request is required")
	}
	oauth, err := gatewaymedia.ImageExecutionPath(account.Record.Type)
	if err != nil {
		return nil, err
	}
	if oauth {
		return s.forwardOpenAIImagesOAuth(ctx, c, account, parsed, channelMappedModel)
	}
	return s.forwardOpenAIImagesAPIKey(ctx, c, account, body, parsed, channelMappedModel)
}

func (s *OpenAIGatewayService) forwardOpenAIImagesAPIKey(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	parsed *gatewaymedia.ImageRequest,
	channelMappedModel string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	startTime := time.Now()
	requestModel, upstreamModel, err := gatewaymedia.ResolveImageModels(parsed.Model, channelMappedModel, "", func(model string) string {
		return gatewayprovider.ExecutionModelPolicy(account).OpenAIUpstream(model, false, false)
	})
	if err != nil {
		return nil, err
	}
	gatewayhttp.SetOpsUpstreamModel(c, upstreamModel)
	logging.LegacyPrintf(
		"service.openai_gateway",
		"[OpenAI] Images request routing request_model=%s upstream_model=%s endpoint=%s account_type=%s",
		strings.TrimSpace(parsed.Model),
		upstreamModel,
		parsed.Endpoint,
		account.Record.Type,
	)
	forwardBody, forwardContentType, err := upstream.RewriteImageModel(body, parsed.ContentType, upstreamModel)
	if err != nil {
		return nil, err
	}
	// 生图是长耗时、上游侧已产生实际成本的操作：客户端中途断开不应连带取消上游请求。
	// gatewayprovider.DetachStreamUpstreamContext 在非流式时原样返回请求 context，于是客户端一断开
	// 就把已经在出图的上游调用打断成 context canceled，网关记 502、不扣费，而上游那边
	// 图已经生成并计费。同一端点的 OAuth 分支 forwardOpenAIImagesOAuth 以及 Grok 媒体
	// 路径本来就无条件脱钩，这里对齐；上游侧仍由 ResponseHeaderTimeout 兜底。
	upstreamCtx, releaseUpstreamCtx := gatewayprovider.DetachUpstreamContext(ctx)
	defer releaseUpstreamCtx()

	token, _, err := s.executionCredentials.Resolve(upstreamCtx, gatewayprovider.ExecutionRecord(account))
	if err != nil {
		return nil, err
	}
	upstreamReq, err := s.buildOpenAIImagesRequest(upstreamCtx, c, account, forwardBody, forwardContentType, token, parsed.Endpoint, tlsRouterMatch...)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}

	options := s.responseOutput.ImageOptions(c)
	options.Backfill = func(body []byte) []byte { return s.backfillOpenAIImagesB64JSON(upstreamCtx, account, parsed, body) }
	var legacyHTTPResult *forwardcore.OpenAIResult
	httpFailure := false
	target := &mediaprovider.ImagesOptions{

		AccountID: account.Record.ID,
		OAuth:     false,
		Model:     upstreamModel,
		StartedAt: startTime,

		Request: upstreamReq,
		Options: options,
		Enter:   s.nativeAttemptActivity,

		ResponseFormat: parsed.ResponseFormat,
		StreamPrefix:   openAIImagesStreamPrefix(parsed),

		Do: func(req *http.Request) (*http.Response, error) {
			upstreamStart := time.Now()
			resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.Record.ID, account.Record.Concurrency, s.Requests.TLSProfile(account, tlsRouterMatch...))
			gatewayhttp.SetOpsLatencyMs(c, gatewayhttp.OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
			return resp, err
		},

		TransportError: func(err error) error {
			safeErr := logredact.SanitizeUpstreamQueries(err.Error())
			gatewayhttp.SetOpsUpstreamError(c, 0, safeErr, "")
			gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
				Platform:           account.Record.Platform,
				AccountID:          account.Record.ID,
				AccountName:        account.Record.Name,
				UpstreamStatusCode: 0,
				UpstreamURL:        logredact.SafeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "request_error",
				Message:            safeErr,
			})
			return fmt.Errorf("upstream request failed: %s", safeErr)
		},

		ReadErrorBody: s.responseOutput.ReadErrorBody,

		RedactErrorBody: func(body []byte) []byte { return s.agentIdentity.Redact(upstreamCtx, account, body) },

		HTTPError: func(resp *http.Response, respBody []byte) error {
			httpFailure = true
			upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
			shouldDisable := false
			_, err := gatewaymedia.ResolveImageFailure(gatewaymedia.ImageFailurePorts{
				Failover: func() bool {
					return gatewayprovider.ShouldFailoverOpenAIResponse(resp.StatusCode, upstreamMsg, respBody)
				},
				Observe: func() {
					gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

						Platform: account.Record.Platform,

						AccountID: account.Record.ID,

						AccountName: account.Record.Name,

						UpstreamStatusCode: resp.StatusCode,

						UpstreamRequestID: resp.Header.Get("x-request-id"),

						UpstreamURL: logredact.SafeUpstreamURL(upstreamReq.URL.String()),

						Kind: "failover",

						Message: upstreamMsg,
					})
				},
				ApplyPolicy: func() bool {
					shouldDisable = s.responseOutput.ApplyHTTPFailure(upstreamCtx, resp, account, respBody, upstreamModel).StopScheduling
					return false
				},
				NewFailover: func() error {
					retryableOnSameAccount := !shouldDisable && account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(resp.StatusCode)
					if account.View().IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
						return (gatewayprovider.OpenAIFailoverPolicy{Health: s.responseOutput.Health}).NewAccountFailure(account, resp.StatusCode, resp.Header, respBody, upstreamMsg, shouldDisable, retryableOnSameAccount)
					}
					if gatewayprovider.IsOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMsg, respBody) {
						return gatewayprovider.NewOpenAIUpstreamFailure(resp.StatusCode, resp.Header, respBody, upstreamMsg, retryableOnSameAccount)
					}
					return &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
				},
				Handle: func() error {
					var failure error
					legacyHTTPResult, failure = s.handleOpenAIImagesErrorResponse(upstreamCtx, resp, c, account, forwardBody, upstreamModel)
					return failure
				},
			})
			return err
		},
	}
	protocolID := protocol.ProtocolImagesGenerations
	if parsed.IsEdits() {
		protocolID = protocol.ProtocolImagesEdits
	}
	result, err := (mediaprovider.Images{Options: *target}).Execute(upstreamCtx, upstream.AttemptInput{Protocol: protocolID, ResponseModel: requestModel, Stream: parsed.Stream}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if httpFailure {
		return legacyHTTPResult, err
	}
	imageCount, retain := gatewaymedia.ImageOutcome(parsed.Stream, false, upstreamopenai.IsEventStreamResponse(result.UpstreamHeaders), parsed.N, result.ObservedImages, err)
	if !retain {
		return nil, err
	}
	return gatewayprovider.ImagesForwardResult(result, parsed, imageCount), err
}

func (s *OpenAIGatewayService) buildOpenAIImagesRequest(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	contentType string,
	token string,
	endpoint string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*http.Request, error) {
	targetURL := openAIImagesGenerationsURL
	if endpoint == upstream.OpenAIImagesEditsEndpoint {
		targetURL = openAIImagesEditsURL
	}
	baseURL := gatewayprovider.ExecutionProtocolTarget(account).GetOpenAIBaseURL()
	if baseURL != "" {
		validatedURL, err := s.Requests.ValidateBaseURL(baseURL)
		if err != nil {
			return nil, err
		}
		targetURL = httpclient.BuildOpenAIEndpointURL(validatedURL, endpoint)
	}

	options := s.Requests.ResponseOptions(ctx, c, account, token, targetURL, false, tlsRouterMatch...)
	options.AllowHeader = func(name string) bool { return gatewayhttp.AllowOpenAIPassthroughHeader(name) }
	return upstreamopenai.BuildImagesRequest(ctx, body, contentType, options)
}

func (s *OpenAIGatewayService) handleOpenAIImagesNonStreamingResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	parsed *gatewaymedia.ImageRequest,
) (openai.ForwardUsage, int, []string, error) {
	options := s.responseOutput.ImageOptions(c)
	options.Backfill = func(body []byte) []byte { return s.backfillOpenAIImagesB64JSON(ctx, account, parsed, body) }
	return upstreamopenai.ReadImagesNonStreaming(resp, gatewayhttp.ResponseSink{Writer: c.Writer}, options)
}

func extractOpenAIImagesBillableCountFromJSONBytes(body []byte) int {
	if count := openai.CountOpenAIResponseImageOutputsFromJSONBytes(body); count > 0 {
		return count
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return 0
	}
	if count := int(gjson.GetBytes(body, "usage.images").Int()); count > 0 {
		return count
	}
	if count := int(gjson.GetBytes(body, "tool_usage.image_gen.images").Int()); count > 0 {
		return count
	}
	eventType := strings.TrimSpace(gjson.GetBytes(body, "type").String())
	if eventType == "" || !strings.HasSuffix(eventType, ".completed") {
		return 0
	}
	if gjson.GetBytes(body, "b64_json").Exists() || gjson.GetBytes(body, "url").Exists() {
		return 1
	}
	return 0
}

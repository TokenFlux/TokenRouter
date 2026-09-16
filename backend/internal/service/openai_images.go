package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	mediaprovider "github.com/TokenFlux/TokenRouter/internal/gateway/media/provider"

	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	nativeupstream "github.com/TokenFlux/TokenRouter/internal/upstream"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/imroc/req/v3"
	"github.com/tidwall/gjson"
)

const (
	openAIImagesGenerationsEndpoint = nativeupstream.OpenAIImagesGenerationsEndpoint
	openAIImagesEditsEndpoint       = nativeupstream.OpenAIImagesEditsEndpoint

	openAIImagesGenerationsURL = "https://api.openai.com/v1/images/generations"
	openAIImagesEditsURL       = "https://api.openai.com/v1/images/edits"

	openAIChatGPTStartURL                  = nativeopenai.OpenAIChatGPTStartURL
	openAIChatGPTFilesURL                  = nativeopenai.OpenAIChatGPTFilesURL
	openAIImageBackendUserAgent            = nativeopenai.OpenAIImageBackendUserAgent
	openAIImageMaxDownloadBytes            = nativeopenai.OpenAIImageMaxDownloadBytes
	openAIImageMaxUploadPartSize           = nativeupstream.OpenAIImageMaxUploadPartSize
	openAIImagesResponsesMainModel         = nativeopenai.ImagesResponsesMainModel
	openAIImagesVerbatimPromptInstructions = nativeopenai.ImagesVerbatimPromptInstructions
)

type OpenAIImagesCapability = accountcore.OpenAIImagesCapability

const (
	OpenAIImagesCapabilityBasic  OpenAIImagesCapability = "images-basic"
	OpenAIImagesCapabilityNative OpenAIImagesCapability = "images-native"
)

type OpenAIImagesUpload = nativeupstream.ImageUpload

type OpenAIImagesRequest = gatewaymedia.ImageRequest

// ParseOpenAIImagesRequest 解析请求并按请求中的模型执行严格校验，供不涉及渠道映射的调用方使用。
func (s *OpenAIGatewayService) ParseOpenAIImagesRequest(c *gin.Context, body []byte) (*OpenAIImagesRequest, error) {
	return s.parseOpenAIImagesRequest(c, body, true)
}

// ParseOpenAIImagesRequestForRouting 解析请求结构，但把模型族校验延后到渠道映射完成之后。
func (s *OpenAIGatewayService) ParseOpenAIImagesRequestForRouting(c *gin.Context, body []byte) (*OpenAIImagesRequest, error) {
	return s.parseOpenAIImagesRequest(c, body, false)
}

func (s *OpenAIGatewayService) parseOpenAIImagesRequest(c *gin.Context, body []byte, validateModel bool) (*OpenAIImagesRequest, error) {
	if c == nil || c.Request == nil {
		return nil, fmt.Errorf("missing request context")
	}
	return gatewaymedia.ParseImageRequest(c.Request.URL.Path, c.GetHeader("Content-Type"), body, validateModel)
}

func applyOpenAIImagesDefaults(req *OpenAIImagesRequest) { gatewaymedia.ApplyImageDefaults(req) }

func isOpenAIImageGenerationModel(model string) bool {
	return gatewaymedia.IsImageGenerationModel(model)
}

func IsGPTImageGenerationModel(model string) bool {
	return gatewaymedia.IsGPTImageGenerationModel(model)
}

func isGrokImageGenerationModel(model string) bool {
	return gatewaymedia.IsGrokImageGenerationModel(model)
}

func normalizeOpenAIImageSizeTier(size string) string {
	return gatewaymedia.NormalizeImageSizeTier(size)
}

func (s *OpenAIGatewayService) ForwardImages(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
	tlsRouterMatch ...TLSFingerprintRouterMatchResult,
) (*OpenAIForwardResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed images request is required")
	}
	oauth, err := gatewaymedia.ImageExecutionPath(account.Type)
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
	account *Account,
	body []byte,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
	tlsRouterMatch ...TLSFingerprintRouterMatchResult,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	requestModel, upstreamModel, err := gatewaymedia.ResolveImageModels(parsed.Model, channelMappedModel, "", func(model string) string {
		return resolveOpenAIAccountUpstreamModelForRequest(account, model, false, false)
	})
	if err != nil {
		return nil, err
	}
	SetOpsUpstreamModel(c, upstreamModel)
	logger.LegacyPrintf(
		"service.openai_gateway",
		"[OpenAI] Images request routing request_model=%s upstream_model=%s endpoint=%s account_type=%s",
		strings.TrimSpace(parsed.Model),
		upstreamModel,
		parsed.Endpoint,
		account.Type,
	)
	forwardBody, forwardContentType, err := rewriteOpenAIImagesModel(body, parsed.ContentType, upstreamModel)
	if err != nil {
		return nil, err
	}
	// 生图是长耗时、上游侧已产生实际成本的操作：客户端中途断开不应连带取消上游请求。
	// detachStreamUpstreamContext 在非流式时原样返回请求 context，于是客户端一断开
	// 就把已经在出图的上游调用打断成 context canceled，网关记 502、不扣费，而上游那边
	// 图已经生成并计费。同一端点的 OAuth 分支 forwardOpenAIImagesOAuth 以及 Grok 媒体
	// 路径本来就无条件脱钩，这里对齐；上游侧仍由 ResponseHeaderTimeout 兜底。
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()

	token, _, err := s.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := s.buildOpenAIImagesRequest(upstreamCtx, c, account, forwardBody, forwardContentType, token, parsed.Endpoint, tlsRouterMatch...)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	options := s.nativeImageResponseOptions(c)
	options.Backfill = func(body []byte) []byte { return s.backfillOpenAIImagesB64JSON(upstreamCtx, account, parsed, body) }
	var legacyHTTPResult *OpenAIForwardResult
	httpFailure := false
	target := &mediaprovider.ImagesOptions{

		AccountID: account.ID,
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
			resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveOpenAITLSProfile(account, tlsRouterMatch...))
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
			return resp, err
		},

		TransportError: func(err error) error {
			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			setOpsUpstreamError(c, 0, safeErr, "")
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: 0,
				UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "request_error",
				Message:            safeErr,
			})
			return fmt.Errorf("upstream request failed: %s", safeErr)
		},

		ReadErrorBody: s.readUpstreamErrorBody,

		RedactErrorBody: func(body []byte) []byte { return s.redactAgentIdentitySensitiveBody(upstreamCtx, account, body) },

		HTTPError: func(resp *http.Response, respBody []byte) error {
			httpFailure = true
			upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
			shouldDisable := false
			_, err := gatewaymedia.ResolveImageFailure(gatewaymedia.ImageFailurePorts{
				Failover: func() bool { return s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody) },
				Observe: func() {
					appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

						Platform: account.Platform,

						AccountID: account.ID,

						AccountName: account.Name,

						UpstreamStatusCode: resp.StatusCode,

						UpstreamRequestID: resp.Header.Get("x-request-id"),

						UpstreamURL: safeUpstreamURL(upstreamReq.URL.String()),

						Kind: "failover",

						Message: upstreamMsg,
					})
				},
				ApplyPolicy: func() bool {
					shouldDisable = s.handleFailoverSideEffects(upstreamCtx, resp, account, respBody, upstreamModel)
					return false
				},
				NewFailover: func() error {
					retryableOnSameAccount := !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)
					if account.IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
						return s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, respBody, upstreamMsg, shouldDisable, retryableOnSameAccount)
					}
					if isOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMsg, respBody) {
						return newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, respBody, upstreamMsg, retryableOnSameAccount)
					}
					return &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
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
	result, err := (mediaprovider.Images{Options: *target}).Execute(upstreamCtx, nativeupstream.AttemptInput{Protocol: protocolID, ResponseModel: requestModel, Stream: parsed.Stream}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if httpFailure {
		return legacyHTTPResult, err
	}
	imageCount, retain := gatewaymedia.ImageOutcome(parsed.Stream, false, isEventStreamResponse(result.UpstreamHeaders), parsed.N, result.ObservedImages, err)
	if !retain {
		return nil, err
	}
	return openAIImagesForwardResult(result, parsed, imageCount), err
}

func (s *OpenAIGatewayService) buildOpenAIImagesRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	contentType string,
	token string,
	endpoint string,
	tlsRouterMatch ...TLSFingerprintRouterMatchResult,
) (*http.Request, error) {
	targetURL := openAIImagesGenerationsURL
	if endpoint == openAIImagesEditsEndpoint {
		targetURL = openAIImagesEditsURL
	}
	baseURL := account.GetOpenAIBaseURL()
	if baseURL != "" {
		validatedURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return nil, err
		}
		targetURL = buildOpenAIImagesURL(validatedURL, endpoint)
	}

	options := s.nativeResponsesRequestOptions(ctx, c, account, token, targetURL, false, tlsRouterMatch...)
	options.AllowHeader = func(name string) bool { return openaiPassthroughAllowedHeaders[name] }
	return nativeopenai.BuildImagesRequest(ctx, body, contentType, options)
}

func buildOpenAIImagesURL(base string, endpoint string) string {
	return buildOpenAIEndpointURL(base, endpoint)
}

func rewriteOpenAIImagesModel(body []byte, contentType string, model string) ([]byte, string, error) {
	return nativeupstream.RewriteImageModel(body, contentType, model)
}

func (s *OpenAIGatewayService) handleOpenAIImagesNonStreamingResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	parsed *OpenAIImagesRequest,
) (OpenAIUsage, int, []string, error) {
	options := s.nativeImageResponseOptions(c)
	options.Backfill = func(body []byte) []byte { return s.backfillOpenAIImagesB64JSON(ctx, account, parsed, body) }
	return nativeopenai.ReadImagesNonStreaming(resp, gatewayhttp.ResponseSink{Writer: c.Writer}, options)
}

func (s *OpenAIGatewayService) openAIImageStreamDataInterval() time.Duration {
	if s == nil || s.cfg == nil || s.cfg.Gateway.ImageStreamDataIntervalTimeout <= 0 {
		return 0
	}
	return time.Duration(s.cfg.Gateway.ImageStreamDataIntervalTimeout) * time.Second
}

func (s *OpenAIGatewayService) openAIImageStreamKeepaliveInterval() time.Duration {
	if s == nil || s.cfg == nil || s.cfg.Gateway.ImageStreamKeepaliveInterval <= 0 {
		return 0
	}
	return time.Duration(s.cfg.Gateway.ImageStreamKeepaliveInterval) * time.Second
}

func extractOpenAIImagesBillableCountFromJSONBytes(body []byte) int {
	if count := extractOpenAIImageCountFromJSONBytes(body); count > 0 {
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

func extractOpenAIImageCountFromJSONBytes(body []byte) int {
	return countOpenAIResponseImageOutputsFromJSONBytes(body)
}

type openAIImagePointerInfo = nativeopenai.ImagePointerInfo

func collectOpenAIImagePointers(body []byte) []openAIImagePointerInfo {
	return nativeopenai.CollectOpenAIImagePointers(body)
}

func resolveOpenAIImageBytes(
	ctx context.Context,
	client *req.Client,
	headers http.Header,
	conversationID string,
	pointer openAIImagePointerInfo,
	errorBodyReadLimit int64,
) ([]byte, error) {
	return nativeopenai.ResolveOpenAIImageBytes(ctx, client, headers, conversationID, pointer, errorBodyReadLimit)
}

func firstNonEmptyString(values ...any) string {
	return nativeopenai.FirstNonEmptyString(values...)
}

type openAIImageStatusError = nativeopenai.ImageStatusError

func newOpenAIImageStatusError(resp *req.Response, fallback string, errorBodyReadLimit int64) error {
	return nativeopenai.NewOpenAIImageStatusError(resp, fallback, errorBodyReadLimit)
}

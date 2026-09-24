package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	mediaprovider "github.com/TokenFlux/TokenRouter/internal/gateway/media/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/gin-gonic/gin"
)

func ExtractGrokMediaModel(contentType string, body []byte) string {
	return gatewayprovider.GrokMediaCodec().ExtractGrokMediaModel(contentType, body)
}

func (s *OpenAIGatewayService) BindGrokMediaVideoRequestAccount(
	ctx context.Context,
	groupID *int64,
	requestID string,
	userID, apiKeyID, accountID int64,
) error {
	return s.MediaVideoTasks().BindGrokMediaVideoRequestAccount(ctx, groupID, requestID, userID, apiKeyID, accountID)
}

func (s *OpenAIGatewayService) ResolveGrokMediaVideoRequestGroup(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) (int64, error) {
	return s.MediaVideoTasks().ResolveGrokMediaVideoRequestGroup(ctx, requestID, userID, apiKeyID)
}

func (s *OpenAIGatewayService) ResolveGrokMediaVideoRequestAccount(
	ctx context.Context,
	groupID *int64,
	requestID string,
	userID, apiKeyID int64,
) (int64, error) {
	return s.MediaVideoTasks().ResolveGrokMediaVideoRequestAccount(ctx, groupID, requestID, userID, apiKeyID)
}

func (s *OpenAIGatewayService) StoreGrokVideoPendingBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
	pending gatewaymedia.GrokVideoPendingBilling,
) error {
	return s.MediaVideoTasks().StoreGrokVideoPendingBilling(ctx, requestID, userID, apiKeyID, pending)
}

func (s *OpenAIGatewayService) LoadGrokVideoPendingBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) (*gatewaymedia.GrokVideoPendingBilling, error) {
	return s.MediaVideoTasks().LoadGrokVideoPendingBilling(ctx, requestID, userID, apiKeyID)
}

func (s *OpenAIGatewayService) ClaimGrokVideoBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) (bool, error) {
	return s.MediaVideoTasks().ClaimGrokVideoBilling(ctx, requestID, userID, apiKeyID)
}

func (s *OpenAIGatewayService) ReleaseGrokVideoBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) error {
	return s.MediaVideoTasks().ReleaseGrokVideoBilling(ctx, requestID, userID, apiKeyID)
}

// xAI 异步视频状态的官方成功结构如下（docs.x.ai Video Generation）：
//
//	示例：{"status":"done","model":"grok-imagine-video-1.5","video":{"url":"...","duration":8,"respect_moderation":true}}
//
// 请求可以包含分辨率（"480p"、"720p" 或 "1080p"），完成状态不会返回该字段，
// 因此计费分辨率取自创建任务时保存的请求快照。

func ExtractGrokVideoBillingFromStatusBody(statusBody []byte, pending *gatewaymedia.GrokVideoPendingBilling, requestID string) *forwardcore.OpenAIResult {
	value := gatewaymedia.ExtractGrokVideoBillingFromStatusBody(statusBody, pending, requestID, extractGrokMediaVideoRequestID(statusBody))
	if value == nil {
		return nil
	}
	return &forwardcore.OpenAIResult{ResponseID: value.ResponseID, Model: value.Model, BillingModel: value.BillingModel, UpstreamModel: value.UpstreamModel, VideoCount: value.VideoCount, VideoResolution: value.VideoResolution, VideoDurationSeconds: value.VideoDurationSeconds}
}

func (s *OpenAIGatewayService) ForwardGrokMedia(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	endpoint grok.GrokMediaEndpoint,
	requestID string,
	body []byte,
	contentType string,
) (*forwardcore.OpenAIResult, error) {
	startTime := time.Now()
	if account == nil {
		return nil, fmt.Errorf("grok account is required")
	}
	if account.Record.Platform != capability.PlatformGrok {
		return nil, fmt.Errorf("account platform %s is not supported for grok media", account.Record.Platform)
	}

	token, _, err := s.requestCredentials.Resolve(ctx, gatewayhttp.RequestCredentialBudget(c), gatewayhttp.CredentialObserver{Context: c}, account)
	if err != nil {
		return nil, err
	}
	if endpoint == grok.GrokMediaEndpointVideoContent {
		return s.forwardGrokMediaVideoContent(ctx, c, account, token, requestID, startTime)
	}
	targetURL, err := buildGrokMediaURL(account, s.cfg, endpoint, requestID)
	if err != nil {
		return nil, err
	}

	body, contentType, err = prepareGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}
	body, contentType, err = normalizeGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}
	requestInfo := gatewayprovider.GrokMediaCodec().ParseGrokMediaRequest(contentType, body)
	billingModel := requestInfo.Model
	upstreamModel := billingModel
	if endpoint.RequiresRequestBody() {
		if mappedModel := strings.TrimSpace(gatewayprovider.ExecutionModelPolicy(account).Mapped(requestInfo.Model)); mappedModel != "" {
			billingModel = mappedModel
		}
		upstreamModel = gatewayprovider.ExecutionModelPolicy(account).NormalizeOpenAI(billingModel)
		if upstreamModel != requestInfo.Model {
			body, contentType, err = gatewayprovider.GrokMediaCodec().RewriteGrokMediaRequestModel(body, contentType, upstreamModel)
			if err != nil {
				return nil, fmt.Errorf("rewrite grok media account mapped model: %w", err)
			}
		}
		modeltrace.RegisterStage(ctx, upstreamModel)
	}
	body, contentType, err = sanitizeGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	var cliHeaders func(http.Header)
	if account.View().IsGrokOAuth() && isGrokCLIProxyTarget(targetURL) {
		cliHeaders = grok.ApplyCLIHeaders
	}
	req, err := grok.BuildMediaRequest(upstreamCtx, endpoint, targetURL, token, contentType, body, cliHeaders, bindAccountHeaders(account))
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}
	handled := false
	var handledResult *forwardcore.OpenAIResult
	target := &mediaprovider.GrokMediaOptions{
		AccountID: account.Record.ID,
		Endpoint:  endpoint,
		Request:   req,
		StartedAt: startTime,
		Enter:     s.nativeAttemptActivity,
		Do: func(req *http.Request) (*http.Response, error) {
			return s.httpUpstream.Do(req, proxyURL, account.Record.ID, account.Record.Concurrency)
		},
		AfterExchange: func(elapsed time.Duration, err error) error {
			gatewayhttp.SetOpsLatencyMs(c, gatewayhttp.OpsUpstreamLatencyMsKey, elapsed.Milliseconds())
			if err != nil {
				return s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
			}
			return nil
		},
		BeforeResponse: func(resp *http.Response) (bool, error) {
			if resp.StatusCode >= 400 {
				handled = true
				var err error
				handledResult, err = s.handleGrokMediaErrorResponse(ctx, resp, c, account, firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")), upstreamModel)
				return true, err
			}
			s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, requestInfo.Model), account, resp.Header, resp.StatusCode)
			return false, nil
		},
		ReadBody: func(reader io.Reader) ([]byte, error) {
			return gatewayhttp.ReadUpstreamResponseBody(reader, resolveUpstreamResponseReadLimit(s.cfg), c, gatewayhttp.OpenAIResponseTooLarge)
		},
		CountImages: openai.CountOpenAIResponseImageOutputsFromJSONBytes,
		TransformBody: func(data []byte) []byte {
			if endpoint == grok.GrokMediaEndpointVideoStatus {
				return rewriteGrokMediaVideoContentURLs(data, requestID, gatewayhttp.GrokMediaContentProxyURL(c, requestID))
			}
			return data
		},
		CopyHeaders: func(dst, src http.Header) { writeOpenAIPassthroughResponseHeaders(dst, src, s.responseHeaderFilter) },
	}
	var sink upstream.OutputSink
	if c != nil {
		sink = gatewayhttp.ResponseSink{Writer: c.Writer}
	}
	protocols := map[grok.GrokMediaEndpoint]protocol.ProtocolID{
		grok.GrokMediaEndpointImagesGenerations: protocol.ProtocolImagesGenerations,
		grok.GrokMediaEndpointImagesEdits:       protocol.ProtocolImagesEdits,
		grok.GrokMediaEndpointVideosGenerations: protocol.ProtocolVideosGenerations,
		grok.GrokMediaEndpointVideosEdits:       protocol.ProtocolVideosEdits,
		grok.GrokMediaEndpointVideosExtensions:  protocol.ProtocolVideosExtensions,
		grok.GrokMediaEndpointVideoStatus:       protocol.ProtocolVideosGenerations,
	}
	result, err := (mediaprovider.GrokMedia{Options: *target}).Execute(upstreamCtx, upstream.AttemptInput{Protocol: protocols[endpoint], Body: body, ResponseModel: requestInfo.Model}, sink)
	if handled {
		return handledResult, err
	}
	if err != nil {
		var missing *grok.MissingImageOutput
		if errors.As(err, &missing) {
			gatewayhttp.SetOpsUpstreamError(c, http.StatusBadGateway, missing.Error(), logredact.TruncateUTF8(string(missing.Body), 512))
			return nil, &forwardcore.UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: missing.Body, ResponseHeaders: missing.Headers}
		}
		return nil, err
	}
	respBody := result.MediaBody

	usage := grokMediaUsageFromResponse(endpoint, requestInfo, respBody)
	resultModel := requestInfo.Model
	resultBillingModel := billingModel
	if endpoint == grok.GrokMediaEndpointVideoStatus {
		// 状态请求不含请求体模型，满足计费条件时使用上游状态字段。
		if m := strings.TrimSpace(usage.Model); m != "" {
			resultModel = m
		}
		if m := strings.TrimSpace(usage.BillingModel); m != "" {
			resultBillingModel = m
		}
	}
	return &forwardcore.OpenAIResult{

		RequestID: result.RequestID,

		UpstreamHeaders: result.UpstreamHeaders,

		ResponseID: usage.ResponseID,

		Usage: usage.Usage,

		Model: resultModel,

		BillingModel: resultBillingModel,

		UpstreamModel: upstreamModel,

		ResponseHeaders: result.UpstreamHeaders.Clone(),

		Duration: time.Since(startTime),

		ImageCount: usage.ImageCount,

		ImageSize: usage.ImageSize,

		ImageInputSize: usage.ImageInputSize,

		ImageOutputSizes: usage.ImageOutputSizes,

		VideoCount: usage.VideoCount,

		VideoResolution: usage.VideoResolution,

		VideoDurationSeconds: usage.VideoDurationSeconds,
	}, nil
}

func (s *OpenAIGatewayService) forwardGrokMediaVideoContent(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	token, requestID string,
	startTime time.Time,
) (*forwardcore.OpenAIResult, error) {
	statusURL, err := buildGrokMediaURL(account, s.cfg, grok.GrokMediaEndpointVideoStatus, requestID)
	if err != nil {
		return nil, err
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}
	rangeHeader := ""
	if c != nil {
		rangeHeader = c.GetHeader("Range")
	}
	handled := false
	var handledResult *forwardcore.OpenAIResult
	resource, err := mediaprovider.OpenVideoContent(upstreamCtx, mediaprovider.VideoContentOptions{
		StatusURL: statusURL,
		RequestID: requestID,
		Token:     token,
		Range:     rangeHeader,
		Context:   upstream.WithHTTPUpstreamRedirectsDisabled,
		ContentURL: func() (string, error) {
			return buildGrokMediaURL(account, s.cfg, grok.GrokMediaEndpointVideoContent, requestID)
		},
		ApplyHeaders: func(headers http.Header, target string) {
			if account.View().IsGrokOAuth() && isGrokCLIProxyTarget(target) {
				grok.ApplyCLIHeaders(headers)
			}
			accountprovider.ApplyAccountHeaderOverrides(gatewayprovider.ExecutionProtocolRecord(account), headers)
		},
		Do: func(req *http.Request) (*http.Response, error) {
			return s.httpUpstream.Do(req, proxyURL, account.Record.ID, account.Record.Concurrency)
		},
		ReadStatus: func(reader io.Reader) ([]byte, error) {
			return gatewayhttp.ReadUpstreamResponseBody(reader, resolveUpstreamResponseReadLimit(s.cfg), c, gatewayhttp.OpenAIResponseTooLarge)
		},
		Latency: func(elapsed time.Duration) {
			gatewayhttp.SetOpsLatencyMs(c, gatewayhttp.OpsUpstreamLatencyMsKey, elapsed.Milliseconds())
		},
		TransportError: func(err error) error { return s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false) },
		HTTPError: func(resp *http.Response, id string) error {
			handled = true
			var err error
			handledResult, err = s.handleGrokMediaErrorResponse(ctx, resp, c, account, id, "")
			return err
		},
		Enter: s.nativeAttemptActivity,
	})
	if handled {
		return handledResult, err
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = resource.Close() }()
	contentResp := &http.Response{StatusCode: resource.StatusCode, ContentLength: resource.ContentLength, Header: resource.Headers, Body: resource}
	contentRequestID := resource.RequestID
	statusBody := resource.StatusBody

	s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, ""), account, contentResp.Header, contentResp.StatusCode)
	if err := writeGrokMediaContentResponse(c, contentResp); err != nil {
		return nil, err
	}
	// 内容下载也是完成观测入口：状态体满足官方 done 和 video.url 条件时附加计费单位，
	// 使处理器能够按与状态轮询相同的路径领取一次计费；待计费快照由处理器合并。
	result := &forwardcore.OpenAIResult{

		RequestID: contentRequestID,

		UpstreamHeaders: contentResp.Header,

		ResponseHeaders: contentResp.Header.Clone(),

		Duration: time.Since(startTime),
	}
	if billed := ExtractGrokVideoBillingFromStatusBody(statusBody, nil, requestID); billed != nil {
		result.ResponseID = firstNonEmpty(billed.ResponseID, strings.TrimSpace(requestID))
		result.Model = billed.Model
		result.BillingModel = billed.BillingModel
		result.UpstreamModel = billed.UpstreamModel
		result.VideoCount = billed.VideoCount
		result.VideoResolution = billed.VideoResolution
		result.VideoDurationSeconds = billed.VideoDurationSeconds
	}
	return result, nil
}

func isGrokCLIProxyTarget(rawURL string) bool {
	return gatewayprovider.GrokMediaCodec().IsGrokCLIProxyTarget(rawURL)
}

func prepareGrokMediaForwardBody(endpoint grok.GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	return gatewayprovider.GrokMediaCodec().PrepareGrokMediaForwardBody(endpoint, body, contentType)
}

func normalizeGrokMediaForwardBody(endpoint grok.GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	return gatewayprovider.GrokMediaCodec().NormalizeGrokMediaForwardBody(endpoint, body, contentType)
}

func sanitizeGrokMediaForwardBody(endpoint grok.GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	return gatewayprovider.GrokMediaCodec().SanitizeGrokMediaForwardBody(endpoint, body, contentType)
}

type grokMediaUsageMetadata struct {
	ResponseID           string
	Usage                openai.ForwardUsage
	Model                string
	BillingModel         string
	ImageCount           int
	ImageSize            string
	ImageInputSize       string
	ImageOutputSizes     []string
	VideoCount           int
	VideoResolution      string
	VideoDurationSeconds int
}

func grokMediaUsageFromResponse(endpoint grok.GrokMediaEndpoint, requestInfo grok.GrokMediaRequestInfo, responseBody []byte) grokMediaUsageMetadata {
	usage, _ := openai.ExtractOpenAIUsageFromJSONBytes(responseBody)
	meta := grokMediaUsageMetadata{Usage: usage}
	switch endpoint {
	case grok.GrokMediaEndpointImagesGenerations, grok.GrokMediaEndpointImagesEdits:
		meta.ImageCount = openai.CountOpenAIResponseImageOutputsFromJSONBytes(responseBody)
		meta.ImageSize = requestInfo.SizeTier
		meta.ImageInputSize = requestInfo.Size
		meta.ImageOutputSizes = openai.CollectOpenAIResponseImageOutputSizesFromJSONBytes(responseBody)
	case grok.GrokMediaEndpointVideosGenerations, grok.GrokMediaEndpointVideosEdits, grok.GrokMediaEndpointVideosExtensions:
		// 异步视频创建阶段只保留任务 ID 和计价参数，完成轮询时再设置可计费数量。
		meta.ResponseID = extractGrokMediaVideoRequestID(responseBody)
		meta.VideoResolution = requestInfo.Resolution
		meta.VideoDurationSeconds = requestInfo.DurationSeconds
	case grok.GrokMediaEndpointVideoStatus:
		// 只有官方完成状态且返回视频地址时，才生成待结算的视频用量。
		if billed := ExtractGrokVideoBillingFromStatusBody(responseBody, nil, ""); billed != nil {
			meta.ResponseID = billed.ResponseID
			meta.Model = billed.Model
			meta.BillingModel = billed.BillingModel
			meta.VideoCount = billed.VideoCount
			meta.VideoResolution = billed.VideoResolution
			meta.VideoDurationSeconds = billed.VideoDurationSeconds
		}
	}
	return meta
}

func extractGrokMediaVideoRequestID(body []byte) string {
	return gatewayprovider.GrokMediaCodec().ExtractGrokMediaVideoRequestID(body)
}

func (s *OpenAIGatewayService) handleGrokMediaErrorResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	requestIDHeader string,
	requestedModel string,
) (*forwardcore.OpenAIResult, error) {
	body := s.readUpstreamErrorBody(resp)
	// 在可配置的透传分支返回前同步账号策略；池模式默认只保留上游观测，不写本地冷却。
	decision := s.applyGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, requestedModel)
	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	if upstreamMsg == "" {
		upstreamMsg = fmt.Sprintf("xAI upstream returned status %d", resp.StatusCode)
	}

	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	gatewayhttp.SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	return nil, gatewaymedia.ResolveGrokFailure(resp.StatusCode, upstreamMsg, gatewaymedia.GrokFailurePorts{
		ContentRejection: func() (bool, string) {
			if !grok.IsGrokContentPolicyRejection(resp.StatusCode, body) {
				return false, ""
			}
			return true, grokContentPolicyClientMessage(body)
		},
		Generic: decision.ShouldReturnGenericError,
		Failover: func() bool {
			return decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, s.shouldFailoverGrokUpstreamError(resp.StatusCode, body))
		},
		Retry: func() gatewaymedia.GrokRetry {
			retryable, delay, deadline, maximum := grokSameAccountRetryMetadata(account, resp.StatusCode, body)
			return gatewaymedia.GrokRetry{Retryable: retryable, PolicyRetryable: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode), Delay: delay, Deadline: deadline, Maximum: maximum}
		},
		Observe: func(kind, message string) {
			gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{Platform: account.Record.Platform, AccountID: account.Record.ID, AccountName: account.Record.Name, UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: requestIDHeader, Kind: kind, Message: message, Detail: upstreamDetail})
		},
		Rewrite: func() (gatewaymedia.ErrorResponse, bool) {
			status, typ, message, matched := gatewayhttp.ApplyErrorPassthroughRule(c, account.Record.Platform, resp.StatusCode, body, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return gatewaymedia.ErrorResponse{Status: status, Type: typ, Message: message}, matched
		},
		Write: func(response gatewaymedia.ErrorResponse) {
			gatewayhttp.MarkResponseCommitted(c)
			gatewayhttp.WriteGrokMediaErrorResponse(c, response.Status, response.Type, response.Message)
		},
		NewFailover: func(retry gatewaymedia.GrokRetry, transient bool) error {
			return &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: body, ResponseHeaders: resp.Header.Clone(), RetryableOnSameAccount: retry.Retryable || retry.PolicyRetryable, RequestScopedTransient: transient, SameAccountRetryDelay: retry.Delay, SameAccountRetryDeadline: retry.Deadline, SameAccountRetryMax: retry.Maximum}
		},
	})
}

func writeGrokMediaContentResponse(c *gin.Context, resp *http.Response) error {
	return gatewayhttp.WriteGrokMediaContentResponse(c, resp, func() { gatewayhttp.MarkResponseCommitted(c) })
}

func rewriteGrokMediaVideoContentURLs(body []byte, requestID, proxyURL string) []byte {
	return gatewaymedia.RewriteVideoContentURLs(body, requestID, proxyURL, gatewayprovider.GrokMediaCodec().IsGrokMediaVideoContentURL)
}

// MediaVideoTasks 投影兼容入口的唯一存储与配置，不创建另一份缓存。
func (s *OpenAIGatewayService) MediaVideoTasks() *gatewaymedia.VideoTasks {
	var owners session.GatewayCache
	var billing session.GrokVideoBillingCache
	var options gatewaymedia.VideoOptions
	if s != nil {
		owners = s.cache
		billing, _ = s.cache.(session.GrokVideoBillingCache)
		if s.cfg != nil {
			options.StickyTTL = time.Duration(s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds) * time.Second
		}
	}
	return gatewaymedia.NewVideoTasks(owners, billing, options)
}

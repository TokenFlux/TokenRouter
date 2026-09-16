package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type GrokMediaEndpoint = nativegrok.GrokMediaEndpoint

const GrokMediaEndpointImagesGenerations = nativegrok.GrokMediaEndpointImagesGenerations
const GrokMediaEndpointImagesEdits = nativegrok.GrokMediaEndpointImagesEdits
const GrokMediaEndpointVideosGenerations = nativegrok.GrokMediaEndpointVideosGenerations
const GrokMediaEndpointVideosEdits = nativegrok.GrokMediaEndpointVideosEdits
const GrokMediaEndpointVideosExtensions = nativegrok.GrokMediaEndpointVideosExtensions
const GrokMediaEndpointVideoStatus = nativegrok.GrokMediaEndpointVideoStatus
const GrokMediaEndpointVideoContent = nativegrok.GrokMediaEndpointVideoContent
const grokMediaMaxEditSourceImages = nativegrok.GrokMediaMaxEditSourceImages

type GrokMediaRequestInfo = nativegrok.GrokMediaRequestInfo

func ExtractGrokMediaModel(contentType string, body []byte) string {
	return grokMediaCodec().ExtractGrokMediaModel(contentType, body)
}

func ParseGrokMediaRequest(contentType string, body []byte) GrokMediaRequestInfo {
	return grokMediaCodec().ParseGrokMediaRequest(contentType, body)
}

func GrokMediaVideoRequestSessionHash(requestID string, userID, apiKeyID int64) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || userID <= 0 || apiKeyID <= 0 {
		return ""
	}
	ownerSeed := fmt.Sprintf("%d:%d:%s", userID, apiKeyID, requestID)
	return "grok-video:" + DeriveSessionHashFromSeed(ownerSeed)
}

const grokMediaVideoRequestOwnerSource = "grok_video_request"

func (s *OpenAIGatewayService) BindGrokMediaVideoRequestAccount(
	ctx context.Context,
	groupID *int64,
	requestID string,
	userID, apiKeyID, accountID int64,
) error {
	if s == nil || s.cache == nil {
		return fmt.Errorf("grok video request binding cache is unavailable")
	}
	sessionHash := GrokMediaVideoRequestSessionHash(requestID, userID, apiKeyID)
	cacheKey := s.openAISessionCacheKey(sessionHash)
	if cacheKey == "" || accountID <= 0 {
		return fmt.Errorf("grok video request binding is invalid")
	}
	// 视频任务可能在 WebSocket 粘性 TTL（默认一小时）之后才完成。
	// 绑定时间至少覆盖待计费快照，确保较晚的状态或内容轮询仍可解析账号。
	ttl := grokVideoPendingBillingTTL(s.cfg)
	if s.cfg != nil && s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds > 0 {
		if sticky := time.Duration(s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds) * time.Second; sticky > ttl {
			ttl = sticky
		}
	}
	if err := s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), cacheKey, accountID, ttl); err != nil {
		return err
	}
	if groupID == nil || *groupID <= 0 {
		return nil
	}
	written, err := s.cache.SetSessionOwnerGroupID(ctx, userID, grokMediaVideoRequestOwnerSource, sessionHash, *groupID, ttl)
	if err != nil {
		return err
	}
	if !written {
		ownerGroupID, getErr := s.cache.GetSessionOwnerGroupID(ctx, userID, grokMediaVideoRequestOwnerSource, sessionHash)
		if getErr != nil {
			return getErr
		}
		if ownerGroupID != *groupID {
			return fmt.Errorf("grok video request binding belongs to another group")
		}
		return s.cache.RefreshSessionOwnerTTL(ctx, userID, grokMediaVideoRequestOwnerSource, sessionHash, ttl)
	}
	return nil
}

// ResolveGrokMediaVideoRequestGroup 返回创建视频任务时保存的分组归属。
func (s *OpenAIGatewayService) ResolveGrokMediaVideoRequestGroup(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) (int64, error) {
	if s == nil || s.cache == nil {
		return 0, fmt.Errorf("grok video request binding cache is unavailable")
	}
	sessionHash := GrokMediaVideoRequestSessionHash(requestID, userID, apiKeyID)
	if sessionHash == "" {
		return 0, fmt.Errorf("grok video request binding is invalid")
	}
	return s.cache.GetSessionOwnerGroupID(ctx, userID, grokMediaVideoRequestOwnerSource, sessionHash)
}

func (s *OpenAIGatewayService) ResolveGrokMediaVideoRequestAccount(
	ctx context.Context,
	groupID *int64,
	requestID string,
	userID, apiKeyID int64,
) (int64, error) {
	if s == nil || s.cache == nil {
		return 0, fmt.Errorf("grok video request binding cache is unavailable")
	}
	cacheKey := s.openAISessionCacheKey(GrokMediaVideoRequestSessionHash(requestID, userID, apiKeyID))
	if cacheKey == "" {
		return 0, fmt.Errorf("grok video request binding is invalid")
	}
	return s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), cacheKey)
}

// GrokVideoPendingBilling 是创建任务时保存的快照，用于状态轮询首次发现已完成视频地址时计费。
// 状态响应可能省略模型或时长，此时先回退到该快照，再回退到默认值。
type GrokVideoPendingBilling struct {
	Model                string `json:"model"`
	BillingModel         string `json:"billing_model,omitempty"`
	UpstreamModel        string `json:"upstream_model,omitempty"`
	VideoResolution      string `json:"video_resolution,omitempty"`
	VideoDurationSeconds int    `json:"video_duration_seconds,omitempty"`
	OriginalModel        string `json:"original_model,omitempty"`
	// CreatedAt 是网关接受异步创建请求的时间，采用 RFC3339Nano UTC 格式。
	// 延迟计费的 duration_ms 从该时刻计算到首次观测到官方 done 和 video.url，
	// 观测来源可以是状态轮询或内容下载，而不是仅计算单次发现请求的耗时。
	CreatedAt string `json:"created_at,omitempty"`
}

// GrokVideoPendingCreatedAtNow 为待计费记录生成任务受理时间戳。
func GrokVideoPendingCreatedAtNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// GrokVideoE2EDuration 返回从任务受理到发现完成的实际经过时间。
// CreatedAt 缺失或无法解析时返回零，由调用方保留仅轮询耗时。
func GrokVideoE2EDuration(createdAt string, discoveredAt time.Time) time.Duration {
	createdAt = strings.TrimSpace(createdAt)
	if createdAt == "" {
		return 0
	}
	if discoveredAt.IsZero() {
		discoveredAt = time.Now()
	}
	var created time.Time
	var err error
	if created, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		if created, err = time.Parse(time.RFC3339, createdAt); err != nil {
			return 0
		}
	}
	if created.IsZero() {
		return 0
	}
	d := discoveredAt.Sub(created)
	if d < 0 {
		return 0
	}
	return d
}

func grokVideoPendingBillingKey(requestID string, userID, apiKeyID int64) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || userID <= 0 || apiKeyID <= 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d:%s", userID, apiKeyID, requestID)
}

func grokVideoPendingBillingTTL(cfg *config.Config) time.Duration {
	// 视频生成可能耗时数分钟，因此将创建时价格保留一天。
	_ = cfg
	return 24 * time.Hour
}

func grokVideoBilledClaimTTL(cfg *config.Config) time.Duration {
	_ = cfg
	return 48 * time.Hour
}

// StoreGrokVideoPendingBilling 持久化创建时计费参数，供状态轮询延迟计费。
func (s *OpenAIGatewayService) StoreGrokVideoPendingBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
	pending GrokVideoPendingBilling,
) error {
	if s == nil || s.cache == nil {
		return fmt.Errorf("grok video pending billing cache is unavailable")
	}
	key := grokVideoPendingBillingKey(requestID, userID, apiKeyID)
	if key == "" {
		return fmt.Errorf("grok video pending billing key is invalid")
	}
	pending.Model = strings.TrimSpace(pending.Model)
	pending.BillingModel = strings.TrimSpace(pending.BillingModel)
	pending.UpstreamModel = strings.TrimSpace(pending.UpstreamModel)
	pending.OriginalModel = strings.TrimSpace(pending.OriginalModel)
	if pending.VideoResolution != "" {
		pending.VideoResolution = NormalizeVideoBillingResolutionOrDefault(pending.VideoResolution)
	}
	if pending.VideoDurationSeconds > 0 {
		pending.VideoDurationSeconds = NormalizeVideoBillingDurationSecondsOrDefault(pending.VideoDurationSeconds)
	}
	// 缺少任务受理时间时始终补写，确保延迟计费的 duration_ms 为端到端耗时。
	if strings.TrimSpace(pending.CreatedAt) == "" {
		pending.CreatedAt = GrokVideoPendingCreatedAtNow()
	} else {
		pending.CreatedAt = strings.TrimSpace(pending.CreatedAt)
	}
	payload, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	cache, ok := s.cache.(GrokVideoBillingCache)
	if !ok {
		return fmt.Errorf("grok video pending billing cache is unavailable")
	}
	return cache.SetGrokVideoPendingBilling(ctx, key, payload, grokVideoPendingBillingTTL(s.cfg))
}

// LoadGrokVideoPendingBilling 返回创建时快照，未命中时可能为 nil。
func (s *OpenAIGatewayService) LoadGrokVideoPendingBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) (*GrokVideoPendingBilling, error) {
	if s == nil || s.cache == nil {
		return nil, fmt.Errorf("grok video pending billing cache is unavailable")
	}
	key := grokVideoPendingBillingKey(requestID, userID, apiKeyID)
	if key == "" {
		return nil, fmt.Errorf("grok video pending billing key is invalid")
	}
	cache, ok := s.cache.(GrokVideoBillingCache)
	if !ok {
		return nil, fmt.Errorf("grok video pending billing cache is unavailable")
	}
	payload, err := cache.GetGrokVideoPendingBilling(ctx, key)
	if err != nil || len(payload) == 0 {
		return nil, err
	}
	var pending GrokVideoPendingBilling
	if err := json.Unmarshal(payload, &pending); err != nil {
		return nil, err
	}
	return &pending, nil
}

// ClaimGrokVideoBilling 对已完成视频请求仅返回一次 true，避免状态轮询重复计费。
// 采用失败关闭策略，领取失败时按已计费处理。
func (s *OpenAIGatewayService) ClaimGrokVideoBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) (bool, error) {
	if s == nil || s.cache == nil {
		return false, fmt.Errorf("grok video billing claim cache is unavailable")
	}
	key := grokVideoPendingBillingKey(requestID, userID, apiKeyID)
	if key == "" {
		return false, fmt.Errorf("grok video billing claim key is invalid")
	}
	cache, ok := s.cache.(GrokVideoBillingCache)
	if !ok {
		return false, fmt.Errorf("grok video billing claim cache is unavailable")
	}
	return cache.ClaimGrokVideoBilled(ctx, key, grokVideoBilledClaimTTL(s.cfg))
}

// ReleaseGrokVideoBilling 在持久化 RecordUsage 失败后释放领取记录，
// 使后续状态或内容轮询能够重试计费。
func (s *OpenAIGatewayService) ReleaseGrokVideoBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) error {
	if s == nil || s.cache == nil {
		return fmt.Errorf("grok video billing claim cache is unavailable")
	}
	key := grokVideoPendingBillingKey(requestID, userID, apiKeyID)
	if key == "" {
		return fmt.Errorf("grok video billing claim key is invalid")
	}
	cache, ok := s.cache.(GrokVideoBillingCache)
	if !ok {
		return fmt.Errorf("grok video billing claim cache is unavailable")
	}
	return cache.ReleaseGrokVideoBilled(ctx, key)
}

// StableGrokVideoBillingRequestID 为单个异步视频任务生成持久化 usage_logs 去重键，
// 该键并非每次轮询各自的网关请求 ID。
func StableGrokVideoBillingRequestID(taskRequestID string) string {
	taskRequestID = strings.TrimSpace(taskRequestID)
	if taskRequestID == "" {
		return ""
	}
	if strings.HasPrefix(taskRequestID, "grok-video:") {
		return taskRequestID
	}
	return "grok-video:" + taskRequestID
}

// xAI 异步视频状态的官方成功结构如下（docs.x.ai Video Generation）：
//
//	示例：{"status":"done","model":"grok-imagine-video-1.5","video":{"url":"...","duration":8,"respect_moderation":true}}
//
// 请求可以包含分辨率（"480p"、"720p" 或 "1080p"），完成状态不会返回该字段，
// 因此计费分辨率取自创建任务时保存的请求快照。

// IsGrokVideoStatusBillable 匹配官方成功条件：status 为 done 且 video.url 非空。
// pending、expired、failed 或缺少视频地址的 done 状态均不可计费。
func IsGrokVideoStatusBillable(statusBody []byte) bool {
	if len(statusBody) == 0 || !gjson.ValidBytes(statusBody) {
		return false
	}
	if !isOfficialGrokVideoStatusDone(statusBody) {
		return false
	}
	return strings.TrimSpace(gjson.GetBytes(statusBody, "video.url").String()) != ""
}

func isOfficialGrokVideoStatusDone(statusBody []byte) bool {
	return grokMediaCodec().IsOfficialGrokVideoStatusDone(statusBody)
}

// ExtractGrokVideoBillingFromStatusBody 根据官方 done 状态构建用量单位。
// 字段优先级遵循官方文档：时长取 video.duration（秒），模型取顶层 model。
//   - 分辨率：状态响应不提供，依次回退到创建时待计费快照和默认 480p
func ExtractGrokVideoBillingFromStatusBody(statusBody []byte, pending *GrokVideoPendingBilling, requestID string) *OpenAIForwardResult {
	if !IsGrokVideoStatusBillable(statusBody) {
		return nil
	}
	model := ""
	billingModel := ""
	upstreamModel := ""
	resolution := ""
	durationSeconds := 0

	if gjson.ValidBytes(statusBody) {
		// 官方模型字段位于顶层。
		model = strings.TrimSpace(gjson.GetBytes(statusBody, "model").String())
		// 官方时长字段为 video.duration，单位是秒。
		if v := gjson.GetBytes(statusBody, "video.duration"); v.Exists() && v.Type == gjson.Number {
			durationSeconds = int(v.Int())
			if durationSeconds == 0 && v.Float() > 0 {
				// 此 API 通常不会返回不足一秒的值，仍接受上方截断后的整数结果。
				durationSeconds = int(v.Float())
			}
		}
	}
	if pending != nil {
		if model == "" {
			model = firstNonEmpty(pending.BillingModel, pending.Model, pending.OriginalModel)
		}
		if billingModel == "" {
			billingModel = firstNonEmpty(pending.BillingModel, pending.Model)
		}
		if upstreamModel == "" {
			upstreamModel = pending.UpstreamModel
		}
		// 官方状态不含分辨率，因此存在创建请求值时始终采用该值。
		resolution = pending.VideoResolution
		if durationSeconds <= 0 {
			durationSeconds = pending.VideoDurationSeconds
		}
	}
	if model == "" {
		// 状态省略模型时使用官方默认视频模型族。
		model = "grok-imagine-video"
	}
	if billingModel == "" {
		billingModel = model
	}
	// 文档约定分辨率仅来自请求；为空时由处理器应用官方默认值 480p。
	if resolution != "" {
		resolution = NormalizeVideoBillingResolutionOrDefault(resolution)
	}
	if durationSeconds > 0 {
		durationSeconds = NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds)
	}
	responseID := extractGrokMediaVideoRequestID(statusBody)
	if responseID == "" {
		responseID = strings.TrimSpace(requestID)
	}
	return &OpenAIForwardResult{

		ResponseID: responseID,

		Model: model,

		BillingModel: billingModel,

		UpstreamModel: upstreamModel,

		VideoCount: 1,

		VideoResolution: resolution,

		VideoDurationSeconds: durationSeconds,
	}
}

func (s *OpenAIGatewayService) ForwardGrokMedia(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	endpoint GrokMediaEndpoint,
	requestID string,
	body []byte,
	contentType string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	if account == nil {
		return nil, fmt.Errorf("grok account is required")
	}
	if account.Platform != PlatformGrok {
		return nil, fmt.Errorf("account platform %s is not supported for grok media", account.Platform)
	}

	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, err
	}
	if endpoint == GrokMediaEndpointVideoContent {
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
	requestInfo := ParseGrokMediaRequest(contentType, body)
	billingModel := requestInfo.Model
	upstreamModel := billingModel
	if endpoint.RequiresRequestBody() {
		if mappedModel := strings.TrimSpace(account.GetMappedModel(requestInfo.Model)); mappedModel != "" {
			billingModel = mappedModel
		}
		upstreamModel = normalizeOpenAIModelForUpstream(account, billingModel)
		if upstreamModel != requestInfo.Model {
			body, contentType, err = RewriteGrokMediaRequestModel(body, contentType, upstreamModel)
			if err != nil {
				return nil, fmt.Errorf("rewrite grok media account mapped model: %w", err)
			}
		}
		RegisterAPIKeyModelRedirectStage(ctx, upstreamModel)
	}
	body, contentType, err = sanitizeGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	var cliHeaders func(http.Header)
	if account.IsGrokOAuth() && isGrokCLIProxyTarget(targetURL) {
		cliHeaders = applyGrokCLIHeaders
	}
	req, err := nativegrok.BuildMediaRequest(upstreamCtx, endpoint, targetURL, token, contentType, body, cliHeaders, account.ApplyHeaderOverrides)
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	handled := false
	var handledResult *OpenAIForwardResult
	target := &nativegrok.MediaTarget{
		AccountID: account.ID,
		Endpoint:  endpoint,
		Request:   req,
		StartedAt: startTime,
		Enter:     s.nativeAttemptActivity,
		Do: func(req *http.Request) (*http.Response, error) {
			return s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
		},
		AfterExchange: func(elapsed time.Duration, err error) error {
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, elapsed.Milliseconds())
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
			return ReadUpstreamResponseBody(reader, s.cfg, c, openAITooLargeError)
		},
		CountImages: countOpenAIResponseImageOutputsFromJSONBytes,
		TransformBody: func(data []byte) []byte {
			if endpoint == GrokMediaEndpointVideoStatus {
				return rewriteGrokMediaVideoContentURLs(data, requestID, grokMediaContentProxyURL(c, requestID))
			}
			return data
		},
		CopyHeaders: func(dst, src http.Header) { writeOpenAIPassthroughResponseHeaders(dst, src, s.responseHeaderFilter) },
	}
	var sink upstream.OutputSink
	if c != nil {
		sink = gatewayhttp.ResponseSink{Writer: c.Writer}
	}
	protocols := map[GrokMediaEndpoint]protocol.ProtocolID{
		GrokMediaEndpointImagesGenerations: protocol.ProtocolImagesGenerations,
		GrokMediaEndpointImagesEdits:       protocol.ProtocolImagesEdits,
		GrokMediaEndpointVideosGenerations: protocol.ProtocolVideosGenerations,
		GrokMediaEndpointVideosEdits:       protocol.ProtocolVideosEdits,
		GrokMediaEndpointVideosExtensions:  protocol.ProtocolVideosExtensions,
		GrokMediaEndpointVideoStatus:       protocol.ProtocolVideosGenerations,
	}
	result, err := (nativegrok.MediaExecutor{}).Execute(upstreamCtx, upstream.AttemptInput{Protocol: protocols[endpoint], Body: body, ResponseModel: requestInfo.Model, Target: target}, sink)
	if handled {
		return handledResult, err
	}
	if err != nil {
		var missing *nativegrok.MissingImageOutput
		if errors.As(err, &missing) {
			setOpsUpstreamError(c, http.StatusBadGateway, missing.Error(), truncateString(string(missing.Body), 512))
			return nil, &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: missing.Body, ResponseHeaders: missing.Headers}
		}
		return nil, err
	}
	respBody := result.MediaBody

	usage := grokMediaUsageFromResponse(endpoint, requestInfo, respBody)
	resultModel := requestInfo.Model
	resultBillingModel := billingModel
	if endpoint == GrokMediaEndpointVideoStatus {
		// 状态请求不含请求体模型，满足计费条件时使用上游状态字段。
		if m := strings.TrimSpace(usage.Model); m != "" {
			resultModel = m
		}
		if m := strings.TrimSpace(usage.BillingModel); m != "" {
			resultBillingModel = m
		}
	}
	return &OpenAIForwardResult{

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

func RewriteGrokMediaRequestModel(body []byte, contentType, model string) ([]byte, string, error) {
	return grokMediaCodec().RewriteGrokMediaRequestModel(body, contentType, model)
}

func (s *OpenAIGatewayService) forwardGrokMediaVideoContent(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	token, requestID string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	statusURL, err := buildGrokMediaURL(account, s.cfg, GrokMediaEndpointVideoStatus, requestID)
	if err != nil {
		return nil, err
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	rangeHeader := ""
	if c != nil {
		rangeHeader = c.GetHeader("Range")
	}
	handled := false
	var handledResult *OpenAIForwardResult
	resource, err := nativegrok.OpenVideoContent(upstreamCtx, nativegrok.VideoContentOptions{
		StatusURL: statusURL,
		RequestID: requestID,
		Token:     token,
		Range:     rangeHeader,
		Context:   WithHTTPUpstreamRedirectsDisabled,
		ContentURL: func() (string, error) {
			return buildGrokMediaURL(account, s.cfg, GrokMediaEndpointVideoContent, requestID)
		},
		ApplyHeaders: func(headers http.Header, target string) {
			if account.IsGrokOAuth() && isGrokCLIProxyTarget(target) {
				applyGrokCLIHeaders(headers)
			}
			account.ApplyHeaderOverrides(headers)
		},
		Do: func(req *http.Request) (*http.Response, error) {
			return s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
		},
		ReadStatus: func(reader io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(reader, s.cfg, c, openAITooLargeError)
		},
		Latency:        func(elapsed time.Duration) { SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, elapsed.Milliseconds()) },
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
	result := &OpenAIForwardResult{

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

func isGrokCLIProxyTarget(rawURL string) bool { return grokMediaCodec().IsGrokCLIProxyTarget(rawURL) }

func prepareGrokMediaForwardBody(endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	return grokMediaCodec().PrepareGrokMediaForwardBody(endpoint, body, contentType)
}

func normalizeGrokMediaForwardBody(endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	return grokMediaCodec().NormalizeGrokMediaForwardBody(endpoint, body, contentType)
}

func sanitizeGrokMediaForwardBody(endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	return grokMediaCodec().SanitizeGrokMediaForwardBody(endpoint, body, contentType)
}

func NormalizeGrokMediaModelForEndpoint(endpoint GrokMediaEndpoint, model string, hasInputImage bool) string {
	return grokMediaCodec().NormalizeGrokMediaModelForEndpoint(endpoint, model, hasInputImage)
}

type grokMediaUsageMetadata struct {
	ResponseID           string
	Usage                OpenAIUsage
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

func grokMediaUsageFromResponse(endpoint GrokMediaEndpoint, requestInfo GrokMediaRequestInfo, responseBody []byte) grokMediaUsageMetadata {
	usage, _ := extractOpenAIUsageFromJSONBytes(responseBody)
	meta := grokMediaUsageMetadata{Usage: usage}
	switch endpoint {
	case GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits:
		meta.ImageCount = countOpenAIResponseImageOutputsFromJSONBytes(responseBody)
		meta.ImageSize = requestInfo.SizeTier
		meta.ImageInputSize = requestInfo.Size
		meta.ImageOutputSizes = collectOpenAIResponseImageOutputSizesFromJSONBytes(responseBody)
	case GrokMediaEndpointVideosGenerations, GrokMediaEndpointVideosEdits, GrokMediaEndpointVideosExtensions:
		// 异步视频创建阶段只保留任务 ID 和计价参数，完成轮询时再设置可计费数量。
		meta.ResponseID = extractGrokMediaVideoRequestID(responseBody)
		meta.VideoResolution = requestInfo.Resolution
		meta.VideoDurationSeconds = requestInfo.DurationSeconds
	case GrokMediaEndpointVideoStatus:
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
	return grokMediaCodec().ExtractGrokMediaVideoRequestID(body)
}

func (s *OpenAIGatewayService) handleGrokMediaErrorResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	requestIDHeader string,
	requestedModel string,
) (*OpenAIForwardResult, error) {
	body := s.readUpstreamErrorBody(resp)
	// 在可配置的透传分支返回前同步账号策略；池模式默认只保留上游观测，不写本地冷却。
	decision := s.applyGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, requestedModel)
	upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if upstreamMsg == "" {
		upstreamMsg = fmt.Sprintf("xAI upstream returned status %d", resp.StatusCode)
	}

	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(body), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	if isGrokContentPolicyRejection(resp.StatusCode, body) {
		clientMsg := grokContentPolicyClientMessage(body)
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

			Platform: account.Platform,

			AccountID: account.ID,

			AccountName: account.Name,

			UpstreamStatusCode: resp.StatusCode,

			UpstreamRequestID: requestIDHeader,

			Kind: "http_error",

			Message: clientMsg,

			Detail: upstreamDetail,
		})
		MarkResponseCommitted(c)
		writeGrokMediaErrorResponse(c, http.StatusForbidden, "invalid_request_error", clientMsg)
		return nil, fmt.Errorf("grok content policy rejection: %s", clientMsg)
	}

	if decision.ShouldReturnGenericError() {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

			Platform: account.Platform,

			AccountID: account.ID,

			AccountName: account.Name,

			UpstreamStatusCode: resp.StatusCode,

			UpstreamRequestID: requestIDHeader,

			Kind: "http_error",

			Message: upstreamMsg,

			Detail: upstreamDetail,
		})
		MarkResponseCommitted(c)
		writeGrokMediaErrorResponse(c, http.StatusInternalServerError, "upstream_error", "Upstream gateway error")
		return nil, fmt.Errorf("upstream error: %d (not in custom error codes) message=%s", resp.StatusCode, upstreamMsg)
	}

	kind := "http_error"
	if decision.ShouldFailover(account, resp.StatusCode, s.shouldFailoverGrokUpstreamError(resp.StatusCode, body)) {
		kind = "failover"
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

		Platform: account.Platform,

		AccountID: account.ID,

		AccountName: account.Name,

		UpstreamStatusCode: resp.StatusCode,

		UpstreamRequestID: requestIDHeader,

		Kind: kind,

		Message: upstreamMsg,

		Detail: upstreamDetail,
	})
	if kind == "failover" {
		retryable, retryDelay, retryDeadline, retryMax := grokSameAccountRetryMetadata(account, resp.StatusCode, body)
		return nil, &UpstreamFailoverError{

			StatusCode: resp.StatusCode,

			ResponseBody: body,

			ResponseHeaders: resp.Header.Clone(),

			RetryableOnSameAccount: retryable || decision.RetryableOnSameAccount(account, resp.StatusCode),

			RequestScopedTransient: retryable && resp.StatusCode == http.StatusTooManyRequests,

			SameAccountRetryDelay: retryDelay,

			SameAccountRetryDeadline: retryDeadline,

			SameAccountRetryMax: retryMax,
		}
	}

	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c,
		account.Platform,
		resp.StatusCode,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		MarkResponseCommitted(c)
		writeGrokMediaErrorResponse(c, status, errType, errMsg)
		return nil, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
	}

	MarkResponseCommitted(c)
	writeGrokMediaErrorResponse(c, resp.StatusCode, grokMediaErrorType(resp.StatusCode), upstreamMsg)
	return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
}

func grokMediaErrorType(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return "upstream_error"
	}
}

func writeGrokMediaErrorResponse(c *gin.Context, statusCode int, errType, message string) {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return
	}
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    strings.TrimSpace(errType),
			"message": strings.TrimSpace(message),
		},
	})
}

func writeGrokMediaContentResponse(c *gin.Context, resp *http.Response) error {
	if c == nil || resp == nil || resp.Body == nil {
		return fmt.Errorf("grok media content response is incomplete")
	}

	for _, name := range []string{
		"Content-Type",
		"Content-Length",
		"Content-Range",
		"Accept-Ranges",
		"Content-Disposition",
	} {
		if value := strings.TrimSpace(resp.Header.Get(name)); value != "" {
			c.Header(name, value)
		}
	}
	if strings.TrimSpace(c.Writer.Header().Get("Content-Length")) == "" && resp.ContentLength >= 0 {
		c.Header("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	if strings.TrimSpace(c.Writer.Header().Get("Content-Type")) == "" {
		c.Header("Content-Type", "application/octet-stream")
	}
	c.Status(resp.StatusCode)
	MarkResponseCommitted(c)
	_, err := io.Copy(c.Writer, resp.Body)
	return err
}

func rewriteGrokMediaVideoContentURLs(body []byte, requestID, proxyURL string) []byte {
	if len(body) == 0 || strings.TrimSpace(requestID) == "" || strings.TrimSpace(proxyURL) == "" || !gjson.ValidBytes(body) {
		return body
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return body
	}
	changed := rewriteGrokMediaKnownVideoURL(&value, proxyURL)
	if rewriteGrokMediaVideoContentURLValue(&value, requestID, proxyURL) {
		changed = true
	}
	if !changed {
		return body
	}
	rewritten, err := json.Marshal(value)
	if err != nil {
		return body
	}
	return rewritten
}

func rewriteGrokMediaKnownVideoURL(value *any, proxyURL string) bool {
	if value == nil {
		return false
	}
	root, ok := (*value).(map[string]any)
	if !ok {
		return false
	}
	video, ok := root["video"].(map[string]any)
	if !ok {
		return false
	}
	rawURL, ok := video["url"].(string)
	if !ok || strings.TrimSpace(rawURL) == "" {
		return false
	}
	video["url"] = proxyURL
	return true
}

func rewriteGrokMediaVideoContentURLValue(value *any, requestID, proxyURL string) bool {
	if value == nil {
		return false
	}
	switch typed := (*value).(type) {
	case map[string]any:
		changed := false
		for key, child := range typed {
			childValue := child
			if rewriteGrokMediaVideoContentURLValue(&childValue, requestID, proxyURL) {
				typed[key] = childValue
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for index, child := range typed {
			childValue := child
			if rewriteGrokMediaVideoContentURLValue(&childValue, requestID, proxyURL) {
				typed[index] = childValue
				changed = true
			}
		}
		return changed
	case string:
		if isGrokMediaVideoContentURL(typed, requestID) {
			*value = proxyURL
			return true
		}
	}
	return false
}

func isGrokMediaVideoContentURL(rawURL, requestID string) bool {
	return grokMediaCodec().IsGrokMediaVideoContentURL(rawURL, requestID)
}

func grokMediaContentProxyURL(c *gin.Context, requestID string) string {
	if c == nil || c.Request == nil || c.Request.URL == nil || strings.TrimSpace(requestID) == "" {
		return ""
	}
	pathPrefix := ""
	if strings.HasPrefix(c.Request.URL.Path, "/v1/") {
		pathPrefix = "/v1"
	}
	return pathPrefix + "/videos/" + url.PathEscape(strings.Trim(requestID, "/")) + "/content"
}

// 输入归一化继续复用 billing/pricing 的原纯规则；不提前读取价格或额外查询。
func grokMediaCodec() nativegrok.MediaCodec {
	return nativegrok.MediaCodec{Options: nativegrok.MediaNormalization{
		MaxUploadPartSize:                             openAIImageMaxUploadPartSize,
		ImageTier1K:                                   ImageBillingSize1K,
		MarshalJSON:                                   marshalOpenAIUpstreamJSON,
		NormalizeImageBillingTierOrDefault:            NormalizeImageBillingTierOrDefault,
		NormalizeVideoBillingResolutionOrDefault:      NormalizeVideoBillingResolutionOrDefault,
		NormalizeVideoBillingDurationSecondsOrDefault: NormalizeVideoBillingDurationSecondsOrDefault,
		ClassifyImageBillingTier:                      ClassifyImageBillingTier,
		ParseImageDimensions:                          parseImageBillingDimensions,
	}}
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	s09bridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	s09openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"

	"github.com/TokenFlux/TokenRouter/internal/util/responseheaders"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

// openaiStreamingResult streaming response result
type openaiStreamingResult struct {
	// 原生执行结果独立报告语义输出、用量存在和 HTTP/重试提交，旧资金入口不读取新增字段。
	served, hasUsage, httpCommitted, retryCommitted, clientDisconnected, observedOnly bool
	firstSemanticOutput                                                               *time.Duration
	usage                                                                             *OpenAIUsage
	firstTokenMs                                                                      *int
	responseID                                                                        string
	imageCount                                                                        int
	imageOutputSizes                                                                  []string
	searchCount                                                                       int
}

type openaiNonStreamingResult struct {
	served bool
	*OpenAIUsage
	usage            *OpenAIUsage
	responseID       string
	imageCount       int
	imageOutputSizes []string
	searchCount      int
}

func (s *OpenAIGatewayService) handleStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, startTime time.Time, originalModel, mappedModel string) (*openaiStreamingResult, error) {
	return s.handleStreamingResponseWithReasoning(ctx, resp, c, account, startTime, originalModel, mappedModel, "")
}

// 旧调用保持失败时的返回约定；新单次执行读取同一解析器保存的已观测结果。
func (s *OpenAIGatewayService) handleStreamingResponseWithReasoning(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, startTime time.Time, originalModel, mappedModel, reasoningEffort string) (*openaiStreamingResult, error) {
	v, err := s.readStreamingResponseObservation(ctx, resp, c, account, startTime, originalModel, mappedModel, reasoningEffort)
	if err != nil && v != nil && v.observedOnly {
		return nil, err
	}
	return v, err
}
func (s *OpenAIGatewayService) readStreamingResponseObservation(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, startTime time.Time, originalModel, mappedModel, reasoningEffort string) (*openaiStreamingResult, error) {
	result, err := nativeopenai.ReadStreamingResponse(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeResponseStreamOptions(ctx, c, account, reasoningEffort), startTime, originalModel, mappedModel, reasoningEffort)
	if result == nil {
		return nil, err
	}
	return &openaiStreamingResult{
		served:              result.Served,
		hasUsage:            result.HasUsage,
		httpCommitted:       result.HttpCommitted,
		retryCommitted:      result.RetryCommitted,
		clientDisconnected:  result.ClientDisconnected,
		observedOnly:        result.ObservedOnly,
		firstSemanticOutput: result.FirstSemanticOutput,
		usage:               result.Usage,
		firstTokenMs:        result.FirstTokenMs,
		responseID:          result.ResponseID,
		imageCount:          result.ImageCount,
		imageOutputSizes:    result.ImageOutputSizes,
		searchCount:         result.SearchCount,
	}, err
}

func extractOpenAISSEDataLine(line string) (string, bool) {
	return s09openai.ExtractSSEDataLine(line)
}

func extractOpenAISSEEventLine(line string) (string, bool) {
	return s09openai.ExtractSSEEventLine(line)
}

type openAICompatSSEFrameParser = s09openai.OpenAICompatSSEFrameParser

func openAICompatPayloadWithEventType(payload, eventType string) string {
	return s09openai.OpenAICompatPayloadWithEventType(payload, eventType)
}

func (s *OpenAIGatewayService) replaceModelInSSELine(line, fromModel, toModel string) string {
	return s09openai.ReplaceModelInSSELine(line, fromModel, toModel)
}

// correctToolCallsInResponseBody 修正响应体中的工具调用
func (s *OpenAIGatewayService) correctToolCallsInResponseBody(body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	updated := body
	if s != nil && s.toolCorrector != nil {
		if corrected, changed := s.toolCorrector.CorrectToolCallsInSSEBytes(updated); changed {
			updated = corrected
		}
	}
	if normalized, changed := normalizeOpenAIResponsesFunctionCallArguments(updated); changed {
		updated = normalized
	}
	return updated
}

func normalizeOpenAIResponsesFunctionCallArguments(data []byte) ([]byte, bool) {
	return s09openai.NormalizeOpenAIResponsesFunctionCallArguments(data)
}

func (s *OpenAIGatewayService) parseSSEUsage(data string, usage *OpenAIUsage) {
	s09openai.ParseSSEUsage(data, usage)
}

func (s *OpenAIGatewayService) parseSSEUsageBytes(data []byte, usage *OpenAIUsage) {
	s09openai.ParseSSEUsageBytes(data, usage)
}

func mergeOpenAIUsageNonZero(dst *OpenAIUsage, src OpenAIUsage) {
	s09openai.MergeOpenAIUsageNonZero(dst, src)
}

func extractOpenAIUsageFromJSONBytes(body []byte) (OpenAIUsage, bool) {
	return s09openai.ExtractOpenAIUsageFromJSONBytes(body)
}

func mergeHostedImageGenToolUsage(imageGen gjson.Result, usage *OpenAIUsage) {
	s09openai.MergeHostedImageGenToolUsage(imageGen, usage)
}

func extractOpenAIResponseIDFromJSONBytes(body []byte) string {
	return s09openai.ExtractOpenAIResponseIDFromJSONBytes(body)
}

const openAIHTTPResponseOwnerContextKey = "openai_http_response_owner"

type openAIHTTPResponseOwner struct {
	userID   int64
	apiKeyID int64
}

// SetOpenAIHTTPResponseOwner marks the authenticated downstream owner whose
// successful Responses IDs may be used for later HTTP continuations.
func SetOpenAIHTTPResponseOwner(c *gin.Context, userID, apiKeyID int64) {
	if c == nil || userID <= 0 || apiKeyID <= 0 {
		return
	}
	c.Set(openAIHTTPResponseOwnerContextKey, openAIHTTPResponseOwner{userID: userID, apiKeyID: apiKeyID})
}

// ValidateOpenAIHTTPResponseOwner authorizes a continuation by downstream
// tenant. API key identity is retained in the binding, while keys owned by the
// same user remain interoperable.
func (s *OpenAIGatewayService) ValidateOpenAIHTTPResponseOwner(
	ctx context.Context,
	groupID int64,
	responseID string,
	userID, apiKeyID int64,
) (bool, error) {
	if s == nil || strings.TrimSpace(responseID) == "" || userID <= 0 || apiKeyID <= 0 {
		return false, nil
	}
	ownerUserID, ownerAPIKeyID, found, err := s.getOpenAIWSStateStore().GetHTTPResponseOwner(ctx, groupID, responseID)
	if err != nil || !found {
		return false, err
	}
	return ownerUserID == userID || (ownerUserID <= 0 && ownerAPIKeyID == apiKeyID), nil
}

// BindOpenAIHTTPResponseOwner records an HTTP continuation owner independently
// from the upstream account selected for that response.
func (s *OpenAIGatewayService) BindOpenAIHTTPResponseOwner(
	ctx context.Context,
	groupID int64,
	responseID string,
	userID, apiKeyID int64,
) error {
	if s == nil {
		return nil
	}
	return s.getOpenAIWSStateStore().BindHTTPResponseOwner(
		ctx, groupID, responseID, userID, apiKeyID, s.openAIWSResponseStickyTTL(),
	)
}

func (s *OpenAIGatewayService) bindHTTPResponseAccount(ctx context.Context, c *gin.Context, account *Account, responseID string) {
	if s == nil || account == nil || account.ID <= 0 {
		return
	}
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return
	}
	store := s.getOpenAIWSStateStore()
	if store == nil {
		return
	}
	groupID := getOpenAIGroupIDFromContext(c)
	ttl := s.openAIWSResponseStickyTTL()
	logOpenAIWSBindResponseAccountWarn(groupID, account.ID, responseID, store.BindResponseAccount(ctx, groupID, responseID, account.ID, ttl))
	if rawOwner, ok := c.Get(openAIHTTPResponseOwnerContextKey); ok {
		if owner, ok := rawOwner.(openAIHTTPResponseOwner); ok && owner.userID > 0 && owner.apiKeyID > 0 {
			if err := s.BindOpenAIHTTPResponseOwner(ctx, groupID, responseID, owner.userID, owner.apiKeyID); err != nil {
				logger.L().Warn(
					"openai.http_bind_response_owner_failed",
					zap.Int64("group_id", groupID),
					zap.Int64("account_id", account.ID),
					zap.Int64("user_id", owner.userID),
					zap.Int64("api_key_id", owner.apiKeyID),
					zap.String("response_id", truncateOpenAIWSLogValue(responseID, openAIWSIDValueMaxLen)),
					zap.Error(err),
				)
			}
		}
	}
}

func (s *OpenAIGatewayService) handleNonStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, originalModel, mappedModel string) (*openaiNonStreamingResult, error) {
	result, err := nativeopenai.ReadNonStreamingResponse(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeNonStreamOptions(ctx, c, account), originalModel, mappedModel)
	if result == nil {
		return nil, err
	}
	return &openaiNonStreamingResult{served: result.Served, OpenAIUsage: result.Usage, usage: result.Usage, responseID: result.ResponseID, imageCount: result.ImageCount, imageOutputSizes: result.ImageOutputSizes, searchCount: result.SearchCount}, err
}

func isEventStreamResponse(header http.Header) bool {
	contentType := strings.ToLower(header.Get("Content-Type"))
	return strings.Contains(contentType, "text/event-stream")
}

func (s *OpenAIGatewayService) handleSSEToJSON(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, body []byte, originalModel, mappedModel string) (*openaiNonStreamingResult, error) {
	result, err := nativeopenai.ReadSSEAsJSON(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeNonStreamOptions(ctx, c, account), body, originalModel, mappedModel)
	if result == nil {
		return nil, err
	}
	return &openaiNonStreamingResult{served: result.Served, OpenAIUsage: result.Usage, usage: result.Usage, responseID: result.ResponseID, imageCount: result.ImageCount, imageOutputSizes: result.ImageOutputSizes, searchCount: result.SearchCount}, err
}

func extractOpenAISSETerminalEvent(body string) (string, []byte, bool) {
	return s09openai.ExtractOpenAISSETerminalEvent(body)
}

func extractOpenAISSEErrorMessage(payload []byte) string {
	return nativeopenai.ExtractOpenAISSEErrorMessage(payload)
}

func (s *OpenAIGatewayService) writeOpenAINonStreamingProtocolError(resp *http.Response, c *gin.Context, message string) error {
	message = sanitizeUpstreamErrorMessage(strings.TrimSpace(message))
	if message == "" {
		message = "Upstream returned an invalid non-streaming response"
	}
	setOpsUpstreamError(c, http.StatusBadGateway, message, "")
	// body-signal compact 心跳可能已把响应头提交为 200，此时只能以
	// response.failed 终止事件回传错误，不能再写 JSON+状态码。
	if openAICompactClientWantsStream(c) && StopOpenAICompactSSEKeepaliveCommitted(c) {
		writeOpenAICompactSSEFailureMessage(c, http.StatusBadGateway, "upstream_error", message)
		return fmt.Errorf("non-streaming openai protocol error: %s", message)
	}
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.JSON(http.StatusBadGateway, gin.H{
		"error": gin.H{
			"type":    "upstream_error",
			"message": message,
		},
	})
	return fmt.Errorf("non-streaming openai protocol error: %s", message)
}

func extractCodexFinalResponse(body string) ([]byte, bool) {
	return s09openai.ExtractCodexFinalResponse(body)
}

func normalizeCompletedImageGenerationStatus(data []byte) ([]byte, bool) {
	return s09openai.NormalizeCompletedImageGenerationStatus(data)
}

type responsesStreamOutputItems = s09bridge.ResponsesStreamOutputItems

func newResponsesStreamOutputItems() *responsesStreamOutputItems {
	return s09bridge.NewResponsesStreamOutputItems()
}

func normalizeResponsesStreamingTerminalOutput(data []byte, acc *apicompat.BufferedResponseAccumulator, doneItems *responsesStreamOutputItems, imageOutputs []json.RawMessage) ([]byte, bool) {
	return s09bridge.NormalizeResponsesStreamingTerminalOutput(data, acc, doneItems, imageOutputs)
}

// supplementCompactionItemFromSSE 保证 compact 请求的终态 output 携带
// compaction item：终态 output 非空但缺失 compaction、而原始事件流的
// output_item.done（或 added）中存在时（上游不一致形态），以 raw JSON 补入。
// Codex remote compact v2 只从 output_item.done 收集 item 且要求恰好一个
// compaction item——纯流式透传（v0.1.146）下客户端直接读事件流天然拿得到，
// SSE→JSON 提取链路必须给出等价结果。非 compact 请求原样返回。
func supplementCompactionItemFromSSE(c *gin.Context, finalResponse []byte, bodyText string) []byte {
	if !isOpenAIResponsesCompactPath(c) {
		return finalResponse
	}
	if len(gjson.GetBytes(finalResponse, "output").Array()) == 0 {
		// 空 output 由 reconstructResponseOutputFromSSE 整体修补，不在此处理。
		return finalResponse
	}
	if responsesOutputHasCompactionItem(finalResponse) {
		return finalResponse
	}
	item, found := findRawCompactionItemFromSSE(bodyText)
	if !found {
		return finalResponse
	}
	patched, err := sjson.SetRawBytes(finalResponse, "output.-1", item)
	if err != nil {
		return finalResponse
	}
	return patched
}

func responsesOutputHasCompactionItem(response []byte) bool {
	return s09openai.ResponsesOutputHasCompactionItem(response)
}

func findRawCompactionItemFromSSE(bodyText string) (json.RawMessage, bool) {
	return s09openai.FindRawCompactionItemFromSSE(bodyText)
}

func reconstructResponseOutputFromSSE(bodyText string) ([]byte, bool) {
	return s09bridge.ReconstructResponseOutputFromSSE(bodyText)
}

func (s *OpenAIGatewayService) replaceModelInSSEBody(body, fromModel, toModel string) string {
	return s09openai.ReplaceModelInSSEBody(body, fromModel, toModel)
}

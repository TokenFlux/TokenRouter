package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	openaierrors "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// IsBareOpenAIResponsesPath 仅匹配裸 /responses 端点（无 /compact 等子路径），
// body-signal 提升只允许发生在这里，避免误伤 /responses/{id}/... 形态的请求。
func IsBareOpenAIResponsesPath(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	normalizedPath := strings.TrimRight(strings.TrimSpace(c.Request.URL.Path), "/")
	switch normalizedPath {
	case EndpointResponses, "/openai/v1/responses", "/responses", "/backend-api/codex/responses":
		return true
	default:
		return false
	}
}

// IsOpenAIRemoteCompactionV2Request 按 wire 形状识别原生 remote compaction v2 流式协议。
func IsOpenAIRemoteCompactionV2Request(body []byte) bool {
	stream, valid := ParseOpenAICompatibleStream(body)
	return valid && stream && protocolopenai.HasCompactionTriggerInInput(body)
}

// normalizeOpenAIResponsesCompactRequest 保留 Codex remote compaction v2 原生的
// 流式 /responses 链路；不满足原生 V2 wire 形状的 body-signal 请求仍提升到旧 compact 桥接链路。
// 返回归一化后的 body；ok=false 表示错误响应已写出，调用方应直接 return。
func (h *OpenAITextHandler) normalizeOpenAIResponsesCompactRequest(c *gin.Context, reqLog *zap.Logger, body []byte) ([]byte, bool) {
	isCompactRequest := IsOpenAIResponsesCompactPath(c)
	if !isCompactRequest && IsBareOpenAIResponsesPath(c) && protocolopenai.HasCompactionTriggerInInput(body) {
		if normalized, changed, err := protocolopenai.NormalizeCompactionTriggerInputOrder(body); err != nil {
			reqLog.Warn("codex.remote_compact.trigger_order_normalization_failed", zap.Error(err))
		} else if changed {
			body = normalized
		}
		if IsOpenAIRemoteCompactionV2Request(body) {
			// 原生 V2 必须在出站前保留协商能力，不能被路径保持逻辑吞掉。
			MarkOpenAINativeCompactionV2(c)
			return body, true
		}
		c.Request.URL.Path = strings.TrimRight(c.Request.URL.Path, "/") + "/compact"
		isCompactRequest = true
		clientStream := gjson.GetBytes(body, "stream").Bool()
		if clientStream {
			MarkOpenAICompactClientStream(c)
		}
		reqLog.Info("codex.remote_compact.detected_body_signal", zap.Bool("client_stream", clientStream))
	}
	if !isCompactRequest {
		return body, true
	}
	if compactSeed := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); compactSeed != "" {
		c.Set(OpenAICompactSessionSeedKey, compactSeed)
	}
	normalizedCompactBody, normalizedCompact, compactErr := openaierrors.NormalizeOpenAICompactRequestBody(body)
	if compactErr != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to normalize compact request body")
		return nil, false
	}
	if normalizedCompact {
		body = normalizedCompactBody
	}
	return body, true
}

func (h *OpenAITextHandler) LogRemoteCompactOutcome(c *gin.Context, startedAt time.Time) {
	if !IsOpenAIResponsesCompactPath(c) {
		return
	}

	var (
		ctx    = context.Background()
		path   string
		status int
	)
	if c != nil {
		if c.Request != nil {
			ctx = c.Request.Context()
			if c.Request.URL != nil {
				path = strings.TrimSpace(c.Request.URL.Path)
			}
		}
		if c.Writer != nil {
			status = c.Writer.Status()
		}
	}

	outcome := "failed"
	if status >= 200 && status < 300 {
		outcome = "succeeded"
	}
	// compact 心跳提交后失败的 wire 状态码固化为 200，真实结局以流内错误
	// 标记为准（response.failed 降级路径会 MarkOpsStreamError）。
	if outcome == "succeeded" && c != nil {
		if _, hasStreamErr := GetOpsStreamError(c); hasStreamErr {
			outcome = "failed"
		}
	}
	latencyMs := time.Since(startedAt).Milliseconds()
	if latencyMs < 0 {
		latencyMs = 0
	}

	fields := []zap.Field{
		zap.String("component", "handler.openai_gateway.responses"),
		zap.Bool("remote_compact", true),
		zap.String("compact_outcome", outcome),
		zap.Int("status_code", status),
		zap.Int64("latency_ms", latencyMs),
		zap.String("path", path),
		zap.Bool("force_codex_cli", h != nil && h.options.ForceCodexCLI),
	}

	if c != nil {
		if userAgent := strings.TrimSpace(c.GetHeader("User-Agent")); userAgent != "" {
			fields = append(fields, zap.String("request_user_agent", userAgent))
		}
		if v, ok := c.Get(OpsModelKey); ok {
			if model, ok := v.(string); ok && strings.TrimSpace(model) != "" {
				fields = append(fields, zap.String("request_model", strings.TrimSpace(model)))
			}
		}
		if v, ok := c.Get(OpsAccountIDKey); ok {
			if accountID, ok := v.(int64); ok && accountID > 0 {
				fields = append(fields, zap.Int64("account_id", accountID))
			}
		}
		if c.Writer != nil {
			if upstreamRequestID := strings.TrimSpace(c.Writer.Header().Get("x-request-id")); upstreamRequestID != "" {
				fields = append(fields, zap.String("upstream_request_id", upstreamRequestID))
			} else if upstreamRequestID := strings.TrimSpace(c.Writer.Header().Get("X-Request-Id")); upstreamRequestID != "" {
				fields = append(fields, zap.String("upstream_request_id", upstreamRequestID))
			}
		}
	}

	log := logging.FromContext(ctx).With(fields...)
	if outcome == "succeeded" {
		log.Info("codex.remote_compact.succeeded")
		return
	}
	log.Warn("codex.remote_compact.failed")
}

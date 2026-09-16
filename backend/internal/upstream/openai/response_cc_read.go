// CC 原生读取供 Responses/Messages 回退复用，回调只处理输出和观察。
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"go.uber.org/zap"
)

// CCResponseOptions 保持每次读取独立的档位观察器及原响应体限制。
type CCResponseOptions struct {
	MaxLineSize               int
	ObserveChunk, ObserveJSON func([]byte)
	ServiceTier               func() string
	ReadBody                  func(io.Reader) ([]byte, error)
	BodyLimitError            error
	WriteError                func(int, string, string)
}

// CCStreamScanState 是 scanCCStream 返回的读取状态快照。
type CCStreamScanState struct {
	// Usage 为 include_usage chunk 中最近一次出现的用量（上游可能重复发送，
	// 总是保留最新值）；终态事件中的用量由调用方在 finalize 阶段自行覆盖。
	Usage wire.ForwardUsage
	// FirstTokenMs 为首个实际输出 chunk（排除 usage-only chunk）的到达时延。
	FirstTokenMs *int
	// ServiceTier 为 SSE chunk 中无歧义的上游实际档位。
	ServiceTier string
	// SawDone 表示上游发出了 [DONE] 哨兵。
	SawDone bool
	// Err 为 scanner 读错误（客户端 context 取消不属于此类，会原样带出）。
	// 非 nil 时调用方必须跳过 finalize 并返回 usage-incomplete 错误，避免
	// 把上游截断伪装成正常收尾。
	Err error
}

// ScanCCStream 保持 CC 分片顺序、原用量覆盖与读取失败处理。
func ScanCCStream(resp *http.Response, options CCResponseOptions, logPrefix, requestID string, startTime time.Time, emit func(*wire.ChatCompletionsChunk)) CCStreamScanState {
	var st CCStreamScanState

	scanner := NewCompatSSEScanner(resp.Body, options.MaxLineSize)
	for scanner.Scan() {
		line := scanner.Text()
		payload, ok := wire.ExtractSSEDataLine(line)
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			st.SawDone = true
			break
		}
		options.ObserveChunk([]byte(payload))
		// 观察上游 CC chunk 回显的 model / service_tier（计费以回显为准）。
		// CC chunk 无 type 字段，按 untyped payload 观察（上游约束：只有终止
		// 事件与无类型 body 报告实际处理档位）。

		if u := wire.ExtractCCStreamUsage(payload); u != nil {
			st.Usage = *u
		}

		var chunk wire.ChatCompletionsChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			logger.L().Warn(logPrefix+": failed to parse chat stream chunk",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
			// 单个无法解析的 chunk 不应阻断后续合法事件；最终工具参数由
			// Responses 转换状态在收尾时单独校验。
			continue
		}
		if st.FirstTokenMs == nil && !wire.IsOpenAIChatUsageOnlyStreamChunk(payload) && wire.ChatChunkStartsResponsesOutput(&chunk) {
			ms := int(time.Since(startTime).Milliseconds())
			st.FirstTokenMs = &ms
		}
		emit(&chunk)
	}
	st.ServiceTier = options.ServiceTier()

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn(logPrefix+": stream read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
		st.Err = err
	}
	return st
}

// LogCCStreamMissingDoneSentinel 保持 CC 分片顺序、原用量覆盖与读取失败处理。
func LogCCStreamMissingDoneSentinel(logPrefix, requestID string) {
	logger.L().Debug(logPrefix+": upstream stream ended without done sentinel",
		zap.String("request_id", requestID),
	)
}

// ReadCCJSONResponse 保持 CC 分片顺序、原用量覆盖与读取失败处理。
func ReadCCJSONResponse(resp *http.Response, options CCResponseOptions) (*wire.ChatCompletionsResponse, wire.ForwardUsage, error) {
	respBody, err := options.ReadBody(resp.Body)
	if err != nil {
		if !errors.Is(err, options.BodyLimitError) {
			options.WriteError(http.StatusBadGateway, "api_error", "Failed to read upstream response")
		}
		return nil, wire.ForwardUsage{}, fmt.Errorf("read upstream body: %w", err)
	}

	var ccResp wire.ChatCompletionsResponse
	if err := json.Unmarshal(respBody, &ccResp); err != nil {
		options.WriteError(http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		return nil, wire.ForwardUsage{}, fmt.Errorf("parse chat completions response: %w", err)
	}
	options.ObserveJSON(respBody)
	// 观察上游 CC JSON 回显的 model / service_tier（计费以回显为准）。
	// CC JSON 无 type 字段，按 untyped payload 观察（上游约束）。

	usage := wire.ForwardUsage{}
	if parsed, ok := wire.ExtractOpenAIUsageFromJSONBytes(respBody); ok {
		usage = parsed
	}
	return &ccResp, usage, nil
}

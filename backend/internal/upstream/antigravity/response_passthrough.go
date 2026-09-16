// 保留原生、兼容与静态上游各自的输出循环；实际写入由同步 sink 承担。
package antigravity

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging" // StreamUpstreamResponse 透传上游 SSE 流并提取 Claude usage
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

func (s *ResponseAdapter) StreamUpstreamResponse(c *upstream.OutputContext, resp *http.Response, startTime time.Time) *StreamResult {
	usage := &upstream.TokenUsage{}
	var firstTokenMs *int
	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := upstream.DefaultSSELineLimit
	if s.Options.MaxLineSize > 0 {
		maxLineSize = s.Options.MaxLineSize
	}
	scanner.Buffer(make([]byte, 64*1024), maxLineSize)

	type scanEvent struct {
		line string
		err  error
	}
	events := make(chan scanEvent, 16)
	done := make(chan struct{})
	sendEvent := func(ev scanEvent) bool {
		select {
		case events <- ev:
			return true
		case <-done:
			return false
		}
	}
	var lastReadAt int64
	atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
	go func() {
		defer close(events)
		for scanner.Scan() {
			atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
			if !sendEvent(scanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = sendEvent(scanEvent{err: err})
		}
	}()
	defer close(done)

	streamInterval := time.Duration(0)
	if s.Options.StreamDataIntervalTimeout > 0 {
		streamInterval = time.Duration(s.Options.StreamDataIntervalTimeout) * time.Second
	}
	var intervalTicker *time.Ticker
	if streamInterval > 0 {
		intervalTicker = time.NewTicker(streamInterval)
		defer intervalTicker.Stop()
	}
	var intervalCh <-chan time.Time
	if intervalTicker != nil {
		intervalCh = intervalTicker.C
	}

	// 下游 keepalive：防止代理/Cloudflare Tunnel 因连接空闲而断开
	keepaliveInterval := time.Duration(0)
	if s.Options.StreamKeepaliveInterval > 0 {
		keepaliveInterval = time.Duration(s.Options.StreamKeepaliveInterval) * time.Second
	}
	var keepaliveTicker *time.Ticker
	if keepaliveInterval > 0 {
		keepaliveTicker = time.NewTicker(keepaliveInterval)
		defer keepaliveTicker.Stop()
	}
	var keepaliveCh <-chan time.Time
	if keepaliveTicker != nil {
		keepaliveCh = keepaliveTicker.C
	}
	lastDataAt := time.Now()

	flusher, _ := c.Writer.(http.Flusher)
	cw := NewClientWriter(c.Writer, flusher, "antigravity upstream")

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: cw.Disconnected()}
			}
			if ev.err != nil {
				if disconnect, handled := HandleStreamReadError(ev.err, cw.Disconnected(), "antigravity upstream"); handled {
					return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: disconnect}
				}
				logger.LegacyPrintf("service.antigravity_gateway", "Stream read error (antigravity upstream): %v", ev.err)
				return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs}
			}

			lastDataAt = time.Now()

			line := ev.line
			s.observeRaw([]byte(line))

			// 记录首 token 时间
			if firstTokenMs == nil && len(line) > 0 {
				ms := int(time.Since(startTime).Milliseconds())
				firstTokenMs = &ms
			}

			// 尝试从 message_delta 或 message_stop 事件提取 usage
			s.ExtractSSEUsage(line, usage)

			// 透传行
			cw.Fprintf("%s\n", line)

		case <-intervalCh:
			lastRead := time.Unix(0, atomic.LoadInt64(&lastReadAt))
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if cw.Disconnected() {
				logger.LegacyPrintf("service.antigravity_gateway", "Upstream timeout after client disconnect (antigravity upstream), returning collected usage")
				return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: true}
			}
			logger.LegacyPrintf("service.antigravity_gateway", "Stream data interval timeout (antigravity upstream)")
			return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs}

		case <-keepaliveCh:
			if cw.Disconnected() {
				continue
			}
			if time.Since(lastDataAt) < keepaliveInterval {
				continue
			}
			// SSE ping 事件：Anthropic 原生格式，客户端会正确处理，
			// 同时保持连接活跃防止 Cloudflare Tunnel 等代理断开
			if !cw.Fprintf("event: ping\ndata: {\"type\": \"ping\"}\n\n") {
				logger.LegacyPrintf("service.antigravity_gateway", "Client disconnected during keepalive ping (antigravity upstream), continuing to drain upstream for billing")
				continue
			}
		}
	}
}

// ExtractSSEUsage 从 SSE data 行中提取 Claude usage（用于流式透传场景）
//
// Anthropic streaming 的 usage 字段分布在两类事件中：
//   - message_start：嵌套在 event.message.usage（input_tokens、cache_creation_input_tokens、
//     cache_read_input_tokens 等输入侧字段）
//   - message_delta：位于顶层 event.usage（流结束时的最终 output_tokens）
//
// 仅读取顶层 event.usage 会漏掉 message_start 的输入侧字段，导致流式透传请求落库的
// usage_logs 记录 input_tokens=0。
func (s *ResponseAdapter) ExtractSSEUsage(line string, usage *upstream.TokenUsage) {
	if !strings.HasPrefix(line, "data: ") {
		return
	}
	dataStr := strings.TrimPrefix(line, "data: ")
	var event map[string]any
	if json.Unmarshal([]byte(dataStr), &event) != nil {
		return
	}
	var u map[string]any
	if eventType, _ := event["type"].(string); eventType == "message_start" {
		if msg, ok := event["message"].(map[string]any); ok {
			u, _ = msg["usage"].(map[string]any)
		}
	} else {
		u, _ = event["usage"].(map[string]any)
	}
	if u == nil {
		return
	}
	if v, ok := u["input_tokens"].(float64); ok && int(v) > 0 {
		usage.InputTokens = int(v)
	}
	if v, ok := u["output_tokens"].(float64); ok && int(v) > 0 {
		usage.OutputTokens = int(v)
	}
	if v, ok := u["cache_read_input_tokens"].(float64); ok && int(v) > 0 {
		usage.CacheReadInputTokens = int(v)
	}
	if v, ok := u["cache_creation_input_tokens"].(float64); ok && int(v) > 0 {
		usage.CacheCreationInputTokens = int(v)
	}
	// 解析嵌套的 cache_creation 对象中的 5m/1h 明细
	if cc, ok := u["cache_creation"].(map[string]any); ok {
		if v, ok := cc["ephemeral_5m_input_tokens"].(float64); ok {
			usage.CacheCreation5mTokens = int(v)
		}
		if v, ok := cc["ephemeral_1h_input_tokens"].(float64); ok {
			usage.CacheCreation1hTokens = int(v)
		}
	}
}

// ExtractClaudeUsage 从非流式 Claude 响应提取 usage
func (s *ResponseAdapter) ExtractClaudeUsage(body []byte) *upstream.TokenUsage {
	usage := &upstream.TokenUsage{}
	var resp map[string]any
	if json.Unmarshal(body, &resp) != nil {
		return usage
	}
	if u, ok := resp["usage"].(map[string]any); ok {
		if v, ok := u["input_tokens"].(float64); ok {
			usage.InputTokens = int(v)
		}
		if v, ok := u["output_tokens"].(float64); ok {
			usage.OutputTokens = int(v)
		}
		if v, ok := u["cache_read_input_tokens"].(float64); ok {
			usage.CacheReadInputTokens = int(v)
		}
		if v, ok := u["cache_creation_input_tokens"].(float64); ok {
			usage.CacheCreationInputTokens = int(v)
		}
		// 解析嵌套的 cache_creation 对象中的 5m/1h 明细
		if cc, ok := u["cache_creation"].(map[string]any); ok {
			if v, ok := cc["ephemeral_5m_input_tokens"].(float64); ok {
				usage.CacheCreation5mTokens = int(v)
			}
			if v, ok := cc["ephemeral_1h_input_tokens"].(float64); ok {
				usage.CacheCreation1hTokens = int(v)
			}
		}
	}
	return usage
}

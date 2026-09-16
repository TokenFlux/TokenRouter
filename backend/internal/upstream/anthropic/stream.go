// 本文件执行 Anthropic SSE 转换；回调只读取外层投影或报告观测。
package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

var sseDataPattern = regexp.MustCompile(`^data:\s*`)

type StreamResult struct {
	Usage            *upstream.TokenUsage
	FirstTokenMs     *int
	ClientDisconnect bool
}
type StreamOptions struct {
	Observe             func(wire.Observation)
	ObserveState        func(*upstream.TokenUsage, *int)
	AccountID           int64
	MaxLineSize         int
	Interval, Keepalive time.Duration
	UserAgent           string
	ToolNames           *ToolNameRewrite
	UpdateWindow        func(context.Context, http.Header)
	WriteHeaders        func(http.Header, http.Header)
	OverrideCache       func(context.Context) (string, bool)
	OnTimeout           func(context.Context, string)
	Failover            func([]byte) error
}

func (o StreamOptions) override(ctx context.Context) (string, bool) {
	if o.OverrideCache == nil {
		return "", false
	}
	return o.OverrideCache(ctx)
}
func (o StreamOptions) failover(body []byte) error {
	if o.Failover != nil {
		return o.Failover(body)
	}
	return &StreamReadFailure{Body: body}
}

type StreamReadFailure struct{ Body []byte }

func (e *StreamReadFailure) Error() string { return "upstream error: 502 (failover)" }
func StreamResponse(ctx context.Context, resp *http.Response, c *upstream.OutputContext, options StreamOptions, startTime time.Time, originalModel, mappedModel string, mimicClaudeCode bool) (*StreamResult, error) {
	// 更新5h窗口状态
	if options.UpdateWindow != nil {
		options.UpdateWindow(ctx, resp.Header)
	}

	if options.WriteHeaders != nil {
		options.WriteHeaders(c.Writer.Header(), resp.Header)
	}

	// 设置SSE响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// 透传其他响应头
	if v := resp.Header.Get("x-request-id"); v != "" {
		c.Header("x-request-id", v)
	}

	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	usage := &upstream.TokenUsage{}
	var firstTokenMs *int
	scanner := bufio.NewScanner(resp.Body)
	// 设置更大的buffer以处理长行
	maxLineSize := options.MaxLineSize
	if maxLineSize <= 0 {
		maxLineSize = upstream.DefaultSSELineLimit
	}
	scanBuf := httpclient.GetSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)

	type scanEvent struct {
		line string
		err  error
	}
	// 独立 goroutine 读取上游，避免读取阻塞导致超时/keepalive无法处理
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
	go func(scanBuf *httpclient.SSEScannerBuf64K) {
		defer httpclient.PutSSEScannerBuf64K(scanBuf)
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
	}(scanBuf)
	defer close(done)

	streamInterval := options.Interval
	// 仅监控上游数据间隔超时，避免下游写入阻塞导致误判
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
	keepaliveInterval := options.Keepalive
	var keepaliveTimer *time.Timer
	if keepaliveInterval > 0 {
		keepaliveTimer = time.NewTimer(keepaliveInterval)
		defer keepaliveTimer.Stop()
	}
	var keepaliveCh <-chan time.Time
	if keepaliveTimer != nil {
		keepaliveCh = keepaliveTimer.C
	}
	lastDataAt := time.Now()
	resetKeepaliveTimer := func() {
		if keepaliveTimer == nil {
			return
		}
		if !keepaliveTimer.Stop() {
			select {
			case <-keepaliveTimer.C:
			default:
			}
		}
		keepaliveTimer.Reset(keepaliveInterval)
	}

	// 仅发送一次错误事件，避免多次写入导致协议混乱（写失败时尽力通知客户端）。
	// 事件格式遵循 Anthropic SSE 标准：{"type":"error","error":{"type":<reason>,"message":<message>}}
	// 这样 Anthropic SDK / Claude Code 等客户端能按标准 error 类型解析，UI 能显示具体错误文案，
	// 服务端 ExtractUpstreamErrorMessage 也能从透传的 body 中提取 message。
	errorEventSent := false
	sendErrorEvent := func(reason, message string) {
		if errorEventSent {
			return
		}
		errorEventSent = true
		if message == "" {
			message = reason
		}
		body, err := json.Marshal(map[string]any{
			"type": "error",
			"error": map[string]string{
				"type":    reason,
				"message": message,
			},
		})
		if err != nil {
			// json.Marshal 不可能在已知 string-only 输入上失败，保守 fallback
			body = []byte(fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":%q}}`, reason, message))
		}
		_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", body)
		flusher.Flush()
	}

	needModelReplace := originalModel != mappedModel
	clientDisconnected := false // 客户端断开标志，断开后继续读取上游以获取完整usage
	sawTerminalEvent := false
	useNoopDeltaKeepalive := ShouldUseClaudeCodeNoopDeltaKeepalive(options.UserAgent)
	noopDeltaKeepaliveBlockIndex := -1
	noopDeltaKeepaliveDeltaType := ""

	pendingEventLines := make([]string, 0, 4)

	processSSEEvent := func(lines []string) ([]string, string, *wire.SseUsagePatch, error) {
		if len(lines) == 0 {
			return nil, "", nil, nil
		}

		eventName := ""
		dataLine := ""
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "event:") {
				eventName = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
				continue
			}
			if dataLine == "" && sseDataPattern.MatchString(trimmed) {
				dataLine = sseDataPattern.ReplaceAllString(trimmed, "")
			}
		}

		if options.Observe != nil {
			options.Observe(wire.ObserveEvent(dataLine))
		}
		if eventName == "error" {
			return nil, dataLine, nil, &StreamErrorEventError{RawData: dataLine}
		}

		if dataLine == "" {
			return []string{strings.Join(lines, "\n") + "\n\n"}, "", nil, nil
		}

		if dataLine == "[DONE]" {
			sawTerminalEvent = true
			block := ""
			if eventName != "" {
				block = "event: " + eventName + "\n"
			}
			block += "data: " + dataLine + "\n\n"
			return []string{block}, dataLine, nil, nil
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(dataLine), &event); err != nil {
			// JSON 解析失败，直接透传原始数据
			block := ""
			if eventName != "" {
				block = "event: " + eventName + "\n"
			}
			block += "data: " + dataLine + "\n\n"
			return []string{block}, dataLine, nil, nil
		}

		eventType, _ := event["type"].(string)
		if eventName == "" {
			eventName = eventType
		}
		eventChanged := false

		if useNoopDeltaKeepalive {
			switch eventType {
			case "content_block_start":
				if idx, ok := SseEventIndex(event); ok {
					noopDeltaKeepaliveBlockIndex = -1
					noopDeltaKeepaliveDeltaType = ""
					if contentBlock, ok := event["content_block"].(map[string]any); ok {
						blockType, _ := contentBlock["type"].(string)
						if deltaType := ClaudeCodeKeepaliveDeltaTypeForContentBlock(blockType); deltaType != "" {
							noopDeltaKeepaliveBlockIndex = idx
							noopDeltaKeepaliveDeltaType = deltaType
						}
					}
				}
			case "content_block_delta":
				if idx, ok := SseEventIndex(event); ok {
					if delta, ok := event["delta"].(map[string]any); ok {
						deltaType, _ := delta["type"].(string)
						if ClaudeCodeKeepaliveFieldForDeltaType(deltaType) != "" {
							noopDeltaKeepaliveBlockIndex = idx
							noopDeltaKeepaliveDeltaType = deltaType
						}
					}
				}
			case "content_block_stop":
				if idx, ok := SseEventIndex(event); ok && idx == noopDeltaKeepaliveBlockIndex {
					noopDeltaKeepaliveBlockIndex = -1
					noopDeltaKeepaliveDeltaType = ""
				}
			case "message_stop":
				noopDeltaKeepaliveBlockIndex = -1
				noopDeltaKeepaliveDeltaType = ""
			}
		}

		// 兼容 Kimi cached_tokens → cache_read_input_tokens
		if eventType == "message_start" {
			if msg, ok := event["message"].(map[string]any); ok {
				if u, ok := msg["usage"].(map[string]any); ok {
					eventChanged = ReconcileCachedTokens(u) || eventChanged
				}
			}
		}
		if eventType == "message_delta" {
			if u, ok := event["usage"].(map[string]any); ok {
				eventChanged = ReconcileCachedTokens(u) || eventChanged
			}
		}

		// Cache TTL Override: 重写 SSE 事件中的 cache_creation 分类。
		// 账号级设置优先；全局 1h 请求注入开启时，默认把 usage 计费归回 5m。
		if overrideTarget, ok := options.override(ctx); ok {
			if eventType == "message_start" {
				if msg, ok := event["message"].(map[string]any); ok {
					if u, ok := msg["usage"].(map[string]any); ok {
						eventChanged = RewriteCacheCreationJSON(u, overrideTarget) || eventChanged
					}
				}
			}
			if eventType == "message_delta" {
				if u, ok := event["usage"].(map[string]any); ok {
					eventChanged = RewriteCacheCreationJSON(u, overrideTarget) || eventChanged
				}
			}
		}

		if needModelReplace {
			if msg, ok := event["message"].(map[string]any); ok {
				if model, ok := msg["model"].(string); ok && model == mappedModel {
					msg["model"] = originalModel
					eventChanged = true
				}
			}
		}

		usagePatch := wire.ExtractSSEUsagePatch(event)
		if StreamEventIsTerminal(eventName, dataLine) {
			sawTerminalEvent = true
		}
		if !eventChanged {
			block := ""
			if eventName != "" {
				block = "event: " + eventName + "\n"
			}
			block += "data: " + dataLine + "\n\n"
			return []string{block}, dataLine, usagePatch, nil
		}

		newData, err := json.Marshal(event)
		if err != nil {
			// 序列化失败，直接透传原始数据
			block := ""
			if eventName != "" {
				block = "event: " + eventName + "\n"
			}
			block += "data: " + dataLine + "\n\n"
			return []string{block}, dataLine, usagePatch, nil
		}

		block := ""
		if eventName != "" {
			block = "event: " + eventName + "\n"
		}
		block += "data: " + string(newData) + "\n\n"
		return []string{block}, string(newData), usagePatch, nil
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				// 上游完成，返回结果
				if !sawTerminalEvent {
					return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: clientDisconnected}, fmt.Errorf("stream usage incomplete: missing terminal event")
				}
				return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: clientDisconnected}, nil
			}
			if ev.err != nil {
				if sawTerminalEvent {
					return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: clientDisconnected}, nil
				}
				// 检测 context 取消（客户端断开会导致 context 取消，进而影响上游读取）
				if errors.Is(ev.err, context.Canceled) || errors.Is(ev.err, context.DeadlineExceeded) {
					return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: true}, fmt.Errorf("stream usage incomplete: %w", ev.err)
				}
				// 客户端已通过写入失败检测到断开，上游也出错了，返回已收集的 usage
				if clientDisconnected {
					return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: true}, fmt.Errorf("stream usage incomplete after disconnect: %w", ev.err)
				}
				// 客户端未断开，正常的错误处理
				if errors.Is(ev.err, bufio.ErrTooLong) {
					logger.LegacyPrintf("service.gateway", "SSE line too long: account=%d max_size=%d error=%v", options.AccountID, maxLineSize, ev.err)
					sendErrorEvent("response_too_large", fmt.Sprintf("upstream SSE line exceeded %d bytes", maxLineSize))
					return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs}, ev.err
				}
				// 上游中途读错误（unexpected EOF / connection reset 等，常见于 HTTP/2 GOAWAY）：
				// 若尚未向客户端写过任何字节，包成 UpstreamFailoverError 让 handler 层走 failover/重试。
				// 已经开始写流时 SSE 协议无 resume，只能透传错误事件给客户端。
				// 注意:面向客户端的 disconnectMsg 必须用 upstream.SanitizeStreamError 剥离地址,
				// 默认 *net.OpError 的 Error() 会泄露内部 IP/端口和上游地址。完整 ev.err
				// 仅在下方 LegacyPrintf 内部日志中保留供运维诊断。
				disconnectMsg := "upstream stream disconnected: " + upstream.SanitizeStreamError(ev.err)
				if !c.Writer.Written() {
					logger.LegacyPrintf("service.gateway", "Upstream stream read error before any client output (account=%d), failing over: %v", options.AccountID, ev.err)
					body, _ := json.Marshal(map[string]any{
						"type": "error",
						"error": map[string]string{
							"type":    "upstream_disconnected",
							"message": disconnectMsg,
						},
					})
					return nil, options.failover(body)
				}
				sendErrorEvent("stream_read_error", disconnectMsg)
				return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs}, fmt.Errorf("stream read error: %w", ev.err)
			}
			line := ev.line
			trimmed := strings.TrimSpace(line)

			if trimmed == "" {
				if len(pendingEventLines) == 0 {
					continue
				}

				outputBlocks, data, usagePatch, err := processSSEEvent(pendingEventLines)
				pendingEventLines = pendingEventLines[:0]
				if err != nil {
					if clientDisconnected {
						return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: true}, nil
					}
					return nil, err
				}
				for _, block := range outputBlocks {
					if !clientDisconnected {
						observation := wire.ObserveEvent(data)
						c.NextEvent(observation.Semantic, observation.Terminal)
						restored := RestoreToolNamesInBytes([]byte(block), options.ToolNames)
						if _, werr := fmt.Fprint(w, string(restored)); werr != nil {
							clientDisconnected = true
							logger.LegacyPrintf("service.gateway", "Client disconnected during streaming, continuing to drain upstream for billing")
							// 不 break：客户端断开后仍需继续合并本事件及后续事件的 usage，
							// 否则会漏计当前事件携带的 usage 导致少计费。后续写入由
							// clientDisconnected 守卫跳过。
						} else {
							flusher.Flush()
							lastDataAt = time.Now()
							resetKeepaliveTimer()
						}
					}
					if data != "" {
						if firstTokenMs == nil && data != "[DONE]" {
							ms := int(time.Since(startTime).Milliseconds())
							firstTokenMs = &ms
						}
						if usagePatch != nil {
							wire.MergeSSEUsagePatch(usage, usagePatch)
						}
						if options.ObserveState != nil {
							options.ObserveState(usage, firstTokenMs)
						}
					}
				}
				continue
			}

			pendingEventLines = append(pendingEventLines, line)

		case <-intervalCh:
			lastRead := time.Unix(0, atomic.LoadInt64(&lastReadAt))
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if clientDisconnected {
				return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs, ClientDisconnect: true}, fmt.Errorf("stream usage incomplete after timeout")
			}
			logger.LegacyPrintf("service.gateway", "Stream data interval timeout: account=%d model=%s interval=%s", options.AccountID, originalModel, streamInterval)
			// 处理流超时，可能标记账户为临时不可调度或错误状态
			if options.OnTimeout != nil {
				options.OnTimeout(ctx, originalModel)
			}
			sendErrorEvent("stream_timeout", fmt.Sprintf("upstream stream idle for %s", streamInterval))
			return &StreamResult{Usage: usage, FirstTokenMs: firstTokenMs}, fmt.Errorf("stream data interval timeout")

		case <-keepaliveCh:
			if clientDisconnected {
				continue
			}
			if time.Since(lastDataAt) < keepaliveInterval {
				resetKeepaliveTimer()
				continue
			}
			keepaliveBlock := "event: ping\ndata: {\"type\": \"ping\"}\n\n"
			if useNoopDeltaKeepalive && noopDeltaKeepaliveBlockIndex >= 0 {
				if block, ok := BuildClaudeCodeNoopDeltaKeepalive(noopDeltaKeepaliveBlockIndex, noopDeltaKeepaliveDeltaType); ok {
					keepaliveBlock = block
				}
			}
			if _, werr := fmt.Fprint(w, keepaliveBlock); werr != nil {
				clientDisconnected = true
				logger.LegacyPrintf("service.gateway", "Client disconnected during keepalive ping, continuing to drain upstream for billing")
				continue
			}
			flusher.Flush()
			lastDataAt = time.Now()
			resetKeepaliveTimer()
		}
	}

}

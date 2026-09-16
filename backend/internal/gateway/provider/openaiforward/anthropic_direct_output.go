// 原生 Messages 输出逐行转发并保留断开后的尾部用量；实际写入只通过 OutputSink。
package openaiforward

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

type NativeAnthropicOptions struct {
	AccountID                         int64
	UpdateWindow                      func(context.Context, http.Header)
	ReadBody                          func(io.Reader) ([]byte, error)
	InvalidJSON                       func(context.Context, *http.Response, []byte, error, string) error
	ForceCache                        func(context.Context) bool
	ClassifyCache                     func([]byte, *upstream.TokenUsage) ([]byte, error)
	CopyHeaders                       func(http.Header, http.Header)
	ReverseTools                      func([]byte) []byte
	MaxLineSize                       func() int
	StreamInterval, KeepaliveInterval func() time.Duration
	ExtractData                       func(string) (string, bool)
	IsTerminal                        func(string, string) bool
	Log                               func(string, ...any)
	HandleTimeout                     func(context.Context, string)
}

func NativeAnthropicBuffered(ctx context.Context, resp *http.Response, c *upstream.OutputContext, o NativeAnthropicOptions, originalModel, billingModel, upstreamModel string, reasoningEffort *string, startTime time.Time) (*Result, error) {
	o.UpdateWindow(ctx, resp.Header)

	body, err := o.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	// fork 当前未启用上游 response-model 观测器；保留原始响应与计费语义。

	var raw json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, o.InvalidJSON(ctx, resp, body, err, billingModel)
	}

	usage := protocolanthropic.ParseClaudeUsageFromResponseBody(body)
	if o.ForceCache(ctx) && usage.InputTokens > 0 {
		body, err = o.ClassifyCache(body, usage)
		if err != nil {
			return nil, err
		}
	}

	o.CopyHeaders(c.Writer.Header(), resp.Header)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	body = o.ReverseTools(body)
	c.Data(resp.StatusCode, contentType, body)

	return &Result{
		RequestID:        resp.Header.Get("x-request-id"),
		Headers:          resp.Header,
		Usage:            AnthropicUsageToOpenAI(usage),
		Model:            originalModel,
		BillingModel:     billingModel,
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: "/v1/messages",
		Stream:           false,
		ReasoningEffort:  reasoningEffort,
		Duration:         time.Since(startTime),
	}, nil
}

func NativeAnthropicStreaming(ctx context.Context, resp *http.Response, c *upstream.OutputContext, o NativeAnthropicOptions, originalModel, billingModel, upstreamModel string, reasoningEffort *string, startTime time.Time) (*Result, error) {
	o.UpdateWindow(ctx, resp.Header)

	o.CopyHeaders(c.Writer.Header(), resp.Header)

	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "text/event-stream"
	}
	c.Header("Content-Type", contentType)
	if c.Writer.Header().Get("Cache-Control") == "" {
		c.Header("Cache-Control", "no-cache")
	}
	if c.Writer.Header().Get("Connection") == "" {
		c.Header("Connection", "keep-alive")
	}
	c.Header("X-Accel-Buffering", "no")
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
	clientDisconnected := false
	sawTerminalEvent := false

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := o.MaxLineSize()
	scanBuf := httpclient.GetSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)

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

	streamInterval := o.StreamInterval()
	var intervalTicker *time.Ticker
	if streamInterval > 0 {
		intervalTicker = time.NewTicker(streamInterval)
		defer intervalTicker.Stop()
	}
	var intervalCh <-chan time.Time
	if intervalTicker != nil {
		intervalCh = intervalTicker.C
	}

	keepaliveInterval := o.KeepaliveInterval()
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
	inPartialEvent := false

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				if !clientDisconnected {
					flusher.Flush()
				}
				if !sawTerminalEvent {
					return NativeAnthropicStreamResult(resp, usage, firstTokenMs, clientDisconnected, originalModel, billingModel, upstreamModel, reasoningEffort, startTime),
						fmt.Errorf("stream usage incomplete: missing terminal event")
				}
				return NativeAnthropicStreamResult(resp, usage, firstTokenMs, clientDisconnected, originalModel, billingModel, upstreamModel, reasoningEffort, startTime), nil
			}
			if ev.err != nil {
				if sawTerminalEvent {
					return NativeAnthropicStreamResult(resp, usage, firstTokenMs, clientDisconnected, originalModel, billingModel, upstreamModel, reasoningEffort, startTime), nil
				}
				if clientDisconnected {
					return NativeAnthropicStreamResult(resp, usage, firstTokenMs, clientDisconnected, originalModel, billingModel, upstreamModel, reasoningEffort, startTime),
						fmt.Errorf("stream usage incomplete after disconnect: %w", ev.err)
				}
				if errors.Is(ev.err, context.Canceled) || errors.Is(ev.err, context.DeadlineExceeded) {
					return NativeAnthropicStreamResult(resp, usage, firstTokenMs, clientDisconnected, originalModel, billingModel, upstreamModel, reasoningEffort, startTime),
						fmt.Errorf("stream usage incomplete: %w", ev.err)
				}
				if errors.Is(ev.err, bufio.ErrTooLong) {
					o.Log("[CN Anthropic 直通] SSE line too long: account=%d max_size=%d error=%v", o.AccountID, maxLineSize, ev.err)
					return NativeAnthropicStreamResult(resp, usage, firstTokenMs, clientDisconnected, originalModel, billingModel, upstreamModel, reasoningEffort, startTime), ev.err
				}
				return NativeAnthropicStreamResult(resp, usage, firstTokenMs, clientDisconnected, originalModel, billingModel, upstreamModel, reasoningEffort, startTime),
					fmt.Errorf("stream read error: %w", ev.err)
			}

			line := ev.line
			if data, ok := o.ExtractData(line); ok {
				trimmed := strings.TrimSpace(data)
				if o.IsTerminal("", trimmed) {
					sawTerminalEvent = true
				}
				if firstTokenMs == nil && trimmed != "" && trimmed != "[DONE]" {
					ms := int(time.Since(startTime).Milliseconds())
					firstTokenMs = &ms
				}
				protocolanthropic.ParseSSEUsagePassthrough(data, usage)
			} else {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "event:") && o.IsTerminal(strings.TrimSpace(strings.TrimPrefix(trimmed, "event:")), "") {
					sawTerminalEvent = true
				}
			}

			if !clientDisconnected {
				restored := string(o.ReverseTools([]byte(line)))
				if _, err := io.WriteString(w, restored); err != nil {
					clientDisconnected = true
					o.Log("[CN Anthropic 直通] Client disconnected during streaming, continue draining upstream for usage: account=%d", o.AccountID)
				} else if _, err := io.WriteString(w, "\n"); err != nil {
					clientDisconnected = true
					o.Log("[CN Anthropic 直通] Client disconnected during streaming, continue draining upstream for usage: account=%d", o.AccountID)
				} else if line == "" {
					// 按 SSE 事件边界刷出，减少每行 flush 带来的 syscall 开销。
					flusher.Flush()
					lastDataAt = time.Now()
					resetKeepaliveTimer()
					inPartialEvent = false
				} else {
					inPartialEvent = true
				}
			}

		case <-intervalCh:
			lastRead := time.Unix(0, atomic.LoadInt64(&lastReadAt))
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if clientDisconnected {
				return NativeAnthropicStreamResult(resp, usage, firstTokenMs, clientDisconnected, originalModel, billingModel, upstreamModel, reasoningEffort, startTime),
					fmt.Errorf("stream usage incomplete after timeout")
			}
			o.Log("[CN Anthropic 直通] Stream data interval timeout: account=%d model=%s interval=%s", o.AccountID, upstreamModel, streamInterval)
			o.HandleTimeout(ctx, upstreamModel)
			return NativeAnthropicStreamResult(resp, usage, firstTokenMs, clientDisconnected, originalModel, billingModel, upstreamModel, reasoningEffort, startTime),
				fmt.Errorf("stream data interval timeout")

		case <-keepaliveCh:
			if clientDisconnected {
				continue
			}
			if inPartialEvent {
				resetKeepaliveTimer()
				continue
			}
			if time.Since(lastDataAt) < keepaliveInterval {
				resetKeepaliveTimer()
				continue
			}
			if _, err := fmt.Fprint(w, "event: ping\ndata: {\"type\": \"ping\"}\n\n"); err != nil {
				clientDisconnected = true
				o.Log("[CN Anthropic 直通] Client disconnected during keepalive ping, continue draining upstream for usage: account=%d", o.AccountID)
				continue
			}
			flusher.Flush()
			lastDataAt = time.Now()
			resetKeepaliveTimer()
		}
	}
}

func NativeAnthropicStreamResult(resp *http.Response, usage *upstream.TokenUsage, firstTokenMs *int, clientDisconnect bool, originalModel, billingModel, upstreamModel string, reasoningEffort *string, startTime time.Time) *Result {
	if usage == nil {
		usage = &upstream.TokenUsage{}
	}
	return &Result{
		RequestID:        resp.Header.Get("x-request-id"),
		Headers:          resp.Header,
		Usage:            AnthropicUsageToOpenAI(usage),
		Model:            originalModel,
		BillingModel:     billingModel,
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: "/v1/messages",
		Stream:           true,
		ReasoningEffort:  reasoningEffort,
		Duration:         time.Since(startTime),
		FirstTokenMs:     firstTokenMs,
		ClientDisconnect: clientDisconnect,
	}
}

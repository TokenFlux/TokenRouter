// API Key 图片流使用独立原生读取路径，不改变 OAuth 与 API Key 的边界差异。
package openai

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// ReadImagesNonStreaming 保留 API Key 图片交付及用量观测的原顺序。
func ReadImagesNonStreaming(resp *http.Response, sink upstream.OutputSink, options ImageResponseOptions) (wire.ForwardUsage, int, []string, error) {
	body, err := options.ReadBody(resp.Body)
	if err != nil {
		return wire.ForwardUsage{}, 0, nil, err
	}
	// 回填只影响交付格式，计费仍使用原始上游用量与图片尺寸。
	usage, _ := wire.ExtractOpenAIUsageFromJSONBytes(body)
	imageCount := wire.CountOpenAIResponseImageOutputsFromJSONBytes(body)
	imageSizes := wire.CollectOpenAIResponseImageOutputSizesFromJSONBytes(body)
	body = options.Backfill(body)
	c := upstream.NewOutputContext(sink)
	options.ResponseHeaders(c.Writer.Header(), resp.Header)
	contentType := "application/json"
	if options.PreserveContentType {
		if upstreamType := resp.Header.Get("Content-Type"); upstreamType != "" {
			contentType = upstreamType
		}
	}
	c.Data(resp.StatusCode, contentType, body)

	return usage, imageCount, imageSizes, nil
}

// ReadImagesStreaming 保留 API Key 图片交付及用量观测的原顺序。
func ReadImagesStreaming(resp *http.Response, c *upstream.OutputContext, options ImageResponseOptions, startTime time.Time) (wire.ForwardUsage, int, []string, *int, error) {
	options.ResponseHeaders(c.Writer.Header(), resp.Header)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "text/event-stream"
	}
	c.Status(resp.StatusCode)
	c.Header("Content-Type", contentType)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return wire.ForwardUsage{}, 0, nil, nil, fmt.Errorf("streaming is not supported by response writer")
	}

	usage := wire.ForwardUsage{}
	imageCounter := wire.NewOpenAIImageOutputCounter()
	var firstTokenMs *int
	clientDisconnected := false
	lastDownstreamWriteAt := time.Now()
	var fallbackBody bytes.Buffer
	fallbackBytes := int64(0)
	fallbackLimit := options.ReadLimit()
	seenSSEData := false
	fallbackTooLarge := false
	var sseData wire.SSEDataAccumulator

	processSSEData := func(dataBytes []byte) {
		seenSSEData = true
		fallbackBody.Reset()
		fallbackBytes = 0
		wire.MergeImageResponseUsage(&usage, dataBytes)
		imageCounter.AddSSEData(dataBytes)
	}

	flushSSEEvent := func() {
		sseData.Flush(processSSEData)
	}

	processLine := func(line []byte) {
		if len(line) == 0 {
			return
		}
		if firstTokenMs == nil {
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}
		if !clientDisconnected {
			if _, writeErr := c.Writer.Write(line); writeErr != nil {
				clientDisconnected = true
				options.Logf("[OpenAI] Images stream client disconnected, continue draining upstream for billing")
			} else {
				flusher.Flush()
				lastDownstreamWriteAt = time.Now()
			}
		}

		trimmedLine := strings.TrimRight(string(line), "\r\n")
		if _, ok := wire.ExtractSSEDataLine(trimmedLine); ok || strings.TrimSpace(trimmedLine) == "" {
			sseData.AddLine(trimmedLine, processSSEData)
			return
		}
		if !seenSSEData && !fallbackTooLarge {
			fallbackBytes += int64(len(line))
			if fallbackBytes <= fallbackLimit {
				_, _ = fallbackBody.Write(line)
			} else {
				fallbackTooLarge = true
				fallbackBody.Reset()
			}
		}
	}

	finalizeFallbackBody := func() {
		if seenSSEData || fallbackBody.Len() == 0 {
			return
		}
		body := bytes.TrimSpace(fallbackBody.Bytes())
		if len(body) == 0 {
			return
		}
		wire.MergeImageResponseUsage(&usage, body)
		imageCounter.AddJSONResponse(body)
	}

	streamInterval := options.StreamInterval()
	keepaliveInterval := options.KeepaliveInterval()
	if streamInterval <= 0 && keepaliveInterval <= 0 {
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadBytes('\n')
			processLine(line)
			if err == io.EOF {
				break
			}
			if err != nil {
				flushSSEEvent()
				return usage, imageCounter.Count(), imageCounter.Sizes(), firstTokenMs, err
			}
		}
		flushSSEEvent()
		finalizeFallbackBody()
		return usage, imageCounter.Count(), imageCounter.Sizes(), firstTokenMs, nil
	}

	type readEvent struct {
		line []byte
		err  error
	}
	events := make(chan readEvent, 16)
	done := make(chan struct{})
	sendEvent := func(ev readEvent) bool {
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
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
			}
			if len(line) > 0 && !sendEvent(readEvent{line: line}) {
				return
			}
			if err == io.EOF {
				return
			}
			if err != nil {
				_ = sendEvent(readEvent{err: err})
				return
			}
		}
	}()
	defer close(done)

	var intervalTicker *time.Ticker
	if streamInterval > 0 {
		intervalTicker = time.NewTicker(streamInterval)
		defer intervalTicker.Stop()
	}
	var intervalCh <-chan time.Time
	if intervalTicker != nil {
		intervalCh = intervalTicker.C
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

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				flushSSEEvent()
				finalizeFallbackBody()
				return usage, imageCounter.Count(), imageCounter.Sizes(), firstTokenMs, nil
			}
			if ev.err != nil {
				flushSSEEvent()
				return usage, imageCounter.Count(), imageCounter.Sizes(), firstTokenMs, ev.err
			}
			processLine(ev.line)
		case <-intervalCh:
			lastRead := time.Unix(0, atomic.LoadInt64(&lastReadAt))
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if clientDisconnected {
				return usage, imageCounter.Count(), imageCounter.Sizes(), firstTokenMs, fmt.Errorf("image stream incomplete after timeout")
			}
			options.Logf("[OpenAI] Images stream data interval timeout: interval=%s", streamInterval)
			_ = WriteImagesStreamEvent(c, options, flusher, "error", BuildOpenAIImagesStreamErrorBody(fmt.Sprintf("upstream image stream idle for %s", streamInterval)))
			return usage, imageCounter.Count(), imageCounter.Sizes(), firstTokenMs, fmt.Errorf("image stream data interval timeout")
		case <-keepaliveCh:
			if clientDisconnected || time.Since(lastDownstreamWriteAt) < keepaliveInterval {
				continue
			}
			if _, writeErr := io.WriteString(c.Writer, ":\n\n"); writeErr != nil {
				clientDisconnected = true
				options.Logf("[OpenAI] Images stream client disconnected during keepalive, continue draining upstream for billing")
				continue
			}
			flusher.Flush()
			lastDownstreamWriteAt = time.Now()
		}
	}
}

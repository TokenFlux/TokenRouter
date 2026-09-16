// 图片响应只执行原生编解码与同步输出，重试及业务副作用由调用方提供。
package openai

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
)

// ImageResponseOptions 只提供本次输出和观测，不保存账号或配置。
type ImageResponseOptions struct {
	PreserveContentType bool
	Backfill            func([]byte) []byte
	ReadLimit           func() int64
	ReadBody            func(io.Reader) ([]byte, error)
	ClassifyReadError   func(error) error
	ResponseHeaders     func(http.Header, http.Header)
	ObserveError        func(int, string, string)
	WriteHTTPError      func(*OpenAIImagesUpstreamError) bool
	EmptyOutput         func([]byte) error
	Summary             func([]byte) string
	AdjustedWrittenSize func() int
	WrittenSize         func() int
	StreamInterval      func() time.Duration
	KeepaliveInterval   func() time.Duration
	Logf                func(string, ...any)
}

// WriteImagesStreamEvent 保留原图片事件、读取和断开收尾时序。
func WriteImagesStreamEvent(c *upstream.OutputContext, options ImageResponseOptions, flusher http.Flusher, eventName string, payload []byte) error {
	if strings.TrimSpace(eventName) != "" {
		if _, err := fmt.Fprintf(c.Writer, "event: %s\n", eventName); err != nil {
			return err
		}
	}
	// 图片事件的语义观测独立于 HTTP 是否已提交，不改变旧计费张数。
	if strings.HasSuffix(eventName, ".partial_image") || strings.HasSuffix(eventName, ".completed") {
		c.NextEvent(true, strings.HasSuffix(eventName, ".completed"))
	}
	if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// TryWriteImagesStreamEvent 保留原图片事件、读取和断开收尾时序。
func TryWriteImagesStreamEvent(
	c *upstream.OutputContext, options ImageResponseOptions,
	flusher http.Flusher,
	clientDisconnected *bool,
	lastWriteAt *time.Time,
	eventName string,
	payload []byte,
) bool {
	if clientDisconnected != nil && *clientDisconnected {
		return false
	}
	if err := WriteImagesStreamEvent(c, options, flusher, eventName, payload); err != nil {
		if clientDisconnected != nil {
			*clientDisconnected = true
		}
		options.Logf("[OpenAI] Images stream client disconnected, continue draining upstream for billing")
		return false
	}
	if lastWriteAt != nil {
		*lastWriteAt = time.Now()
	}
	return true
}

// ReadImagesOAuthNonStreaming 保留原图片事件、读取和断开收尾时序。
func ReadImagesOAuthNonStreaming(
	resp *http.Response,
	sink upstream.OutputSink, options ImageResponseOptions,
	responseFormat string,
	fallbackModel string,
) (wire.ForwardUsage, int, []string, error) {
	body, err := options.ReadBody(resp.Body)
	if err != nil {
		err = options.ClassifyReadError(err)
		return wire.ForwardUsage{}, 0, nil, err
	}

	var usage wire.ForwardUsage
	wire.ForEachSSEDataPayload(string(body), func(data []byte) {
		ParseOpenAIImagesSSEUsageBytes(data, &usage)
	})
	results, createdAt, usageRaw, firstMeta, _, err := CollectOpenAIImagesFromResponsesBody(body, time.Now)
	if err != nil {
		return wire.ForwardUsage{}, 0, nil, err
	}
	if len(results) == 0 {
		if upstreamErr := ExtractOpenAIImagesUpstreamError(body); upstreamErr != nil {
			options.ObserveError(upstreamErr.ClientStatusCode(), upstreamErr.ClientMessage(), "")
			if !IsOpenAIImagesRetryableUpstreamError(upstreamErr) {
				options.WriteHTTPError(upstreamErr)
			}
			return wire.ForwardUsage{}, 0, nil, upstreamErr
		}
		if textFallbackErr := OpenAIImagesTextFallbackError(body); textFallbackErr != nil {
			options.ObserveError(textFallbackErr.ClientStatusCode(), textFallbackErr.ClientMessage(), options.Summary(body))
			if !IsOpenAIImagesRetryableUpstreamError(textFallbackErr) {
				options.WriteHTTPError(textFallbackErr)
			}
			return wire.ForwardUsage{}, 0, nil, textFallbackErr
		}
		// 真空响应：既无图也无文字输出，保持短暂可重试语义并优先同账号重试。
		options.ObserveError(http.StatusBadGateway, "upstream did not return image output", options.Summary(body))
		return wire.ForwardUsage{}, 0, nil, options.EmptyOutput(body)
	}
	if strings.TrimSpace(firstMeta.Model) == "" {
		firstMeta.Model = strings.TrimSpace(fallbackModel)
	}

	responseBody, err := BuildOpenAIImagesAPIResponse(results, createdAt, usageRaw, firstMeta, responseFormat, time.Now)
	if err != nil {
		return wire.ForwardUsage{}, 0, nil, err
	}
	c := upstream.NewOutputContext(sink)
	options.ResponseHeaders(c.Writer.Header(), resp.Header)
	c.Data(resp.StatusCode, "application/json; charset=utf-8", responseBody)
	return usage, len(results), OpenAIResponsesImageResultSizes(results), nil
}

// ReadImagesOAuthStreaming 保留原图片事件、读取和断开收尾时序。
func ReadImagesOAuthStreaming(
	resp *http.Response,
	c *upstream.OutputContext, options ImageResponseOptions,
	startTime time.Time,
	responseFormat string,
	streamPrefix string,
	fallbackModel string,
) (wire.ForwardUsage, int, []string, *int, error) {
	options.ResponseHeaders(c.Writer.Header(), resp.Header)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(resp.StatusCode)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return wire.ForwardUsage{}, 0, nil, nil, fmt.Errorf("streaming is not supported by response writer")
	}

	format := strings.ToLower(strings.TrimSpace(responseFormat))
	if format == "" {
		format = "b64_json"
	}

	usage := wire.ForwardUsage{}
	imageCount := 0
	var imageOutputSizes []string
	var firstTokenMs *int
	emitted := make(map[string]struct{})
	pendingResults := make([]OpenAIResponsesImageResult, 0, 1)
	pendingSeen := make(map[string]struct{})
	streamMeta := OpenAIResponsesImageResult{Model: strings.TrimSpace(fallbackModel)}
	var fallbackText strings.Builder
	appendFallbackText := func(text string) {
		if text == "" || fallbackText.Len() >= 600 {
			return
		}
		remaining := 600 - fallbackText.Len()
		if len(text) > remaining {
			text = text[:remaining]
		}
		_, _ = fallbackText.WriteString(text)
	}
	var createdAt int64
	clientDisconnected := false
	lastDownstreamWriteAt := time.Now()
	var sseData wire.SSEDataAccumulator
	var processDataErr error
	processDataDone := false
	writerSizeBeforeResponse := options.AdjustedWrittenSize()

	processData := func(dataBytes []byte) {
		if processDataDone || processDataErr != nil {
			return
		}
		if firstTokenMs == nil {
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}
		ParseOpenAIImagesSSEUsageBytes(dataBytes, &usage)
		if !gjson.ValidBytes(dataBytes) {
			return
		}
		if meta, eventCreatedAt, ok := ExtractOpenAIResponsesImageMetaFromLifecycleEvent(dataBytes); ok {
			MergeOpenAIResponsesImageMeta(&streamMeta, meta)
			if eventCreatedAt > 0 {
				createdAt = eventCreatedAt
			}
		}
		if gjson.GetBytes(dataBytes, "type").String() == "response.output_text.delta" {
			appendFallbackText(gjson.GetBytes(dataBytes, "delta").String())
		}
		switch gjson.GetBytes(dataBytes, "type").String() {
		case "response.image_generation_call.partial_image":
			b64 := strings.TrimSpace(gjson.GetBytes(dataBytes, "partial_image_b64").String())
			if b64 == "" {
				return
			}
			eventName := streamPrefix + ".partial_image"
			partialMeta := streamMeta
			MergeOpenAIResponsesImageMeta(&partialMeta, OpenAIResponsesImageResult{
				OutputFormat: strings.TrimSpace(gjson.GetBytes(dataBytes, "output_format").String()),
				Background:   strings.TrimSpace(gjson.GetBytes(dataBytes, "background").String()),
			})
			payload := BuildOpenAIImagesStreamPartialPayload(
				eventName,
				b64,
				gjson.GetBytes(dataBytes, "partial_image_index").Int(),
				format,
				createdAt,
				partialMeta, time.Now)
			TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, eventName, payload)
		case "response.output_item.done":
			img, itemID, ok, extractErr := ExtractOpenAIImageFromResponsesOutputItemDone(dataBytes)
			if extractErr != nil {
				TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBody(extractErr.Error()))
				processDataErr = extractErr
				processDataDone = true
				return
			}
			if !ok {
				return
			}
			MergeOpenAIResponsesImageMeta(&streamMeta, img)
			MergeOpenAIResponsesImageMeta(&img, streamMeta)
			key := OpenAIResponsesImageResultKey(itemID, img)
			if _, exists := emitted[key]; exists {
				return
			}
			if _, exists := pendingSeen[key]; exists {
				return
			}
			pendingSeen[key] = struct{}{}
			pendingResults = append(pendingResults, img)
		case "response.completed":
			results, _, usageRaw, firstMeta, extractErr := ExtractOpenAIImagesFromResponsesCompleted(dataBytes, time.Now)
			if extractErr != nil {
				TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBody(extractErr.Error()))
				processDataErr = extractErr
				processDataDone = true
				return
			}
			MergeOpenAIResponsesImageMeta(&streamMeta, firstMeta)
			finalResults := make([]OpenAIResponsesImageResult, 0, len(results)+len(pendingResults))
			finalSeen := make(map[string]struct{})
			for _, img := range results {
				MergeOpenAIResponsesImageMeta(&img, streamMeta)
				AppendOpenAIResponsesImageResultDedup(&finalResults, finalSeen, "", img)
			}
			for _, img := range pendingResults {
				MergeOpenAIResponsesImageMeta(&img, streamMeta)
				AppendOpenAIResponsesImageResultDedup(&finalResults, finalSeen, "", img)
			}
			ReconcileOpenAIResponsesImageResultSizes(finalResults, nil)
			if len(finalResults) == 0 {
				textFallbackErr := OpenAIImagesTextFallbackErrorForText(fallbackText.String())
				if textFallbackErr == nil {
					textFallbackErr = OpenAIImagesTextFallbackError(dataBytes)
				}
				if textFallbackErr != nil {
					retryable := IsOpenAIImagesRetryableUpstreamError(textFallbackErr)
					options.ObserveError(textFallbackErr.ClientStatusCode(), textFallbackErr.ClientMessage(), SummarizeOpenAIImagesNoOutputBody(dataBytes))
					if !retryable && !clientDisconnected {
						TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBodyFromUpstream(textFallbackErr))
					}
					processDataErr = textFallbackErr
					processDataDone = true
					return
				}
				outputErr := fmt.Errorf("upstream did not return image output")
				// 软失败：终态没有图片且没有可分类文本时，记录诊断摘要并返回可重试错误。
				options.ObserveError(http.StatusBadGateway, "upstream did not return image output", options.Summary(dataBytes))
				TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBody(outputErr.Error()))
				processDataErr = outputErr
				processDataDone = true
				return
			}
			eventName := streamPrefix + ".completed"
			for _, img := range finalResults {
				key := OpenAIResponsesImageResultKey("", img)
				if _, exists := emitted[key]; exists {
					continue
				}
				payload := BuildOpenAIImagesStreamCompletedPayload(eventName, img, format, createdAt, usageRaw, time.Now)
				emitted[key] = struct{}{}
				TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, eventName, payload)
			}
			imageCount = len(emitted)
			imageOutputSizes = OpenAIResponsesImageResultSizes(finalResults)
			processDataDone = true
		case "error", "response.failed":
			if upstreamErr := OpenAIImagesUpstreamErrorFromSSEPayload(dataBytes); upstreamErr != nil {
				retryable := IsOpenAIImagesRetryableUpstreamError(upstreamErr)
				if !clientDisconnected && (!retryable || options.WrittenSize() != writerSizeBeforeResponse) {
					TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBodyFromUpstream(upstreamErr))
				}
				options.ObserveError(upstreamErr.ClientStatusCode(), upstreamErr.ClientMessage(), "")
				processDataErr = upstreamErr
				processDataDone = true
				return
			}
		}
	}

	processLine := func(line []byte) (bool, error) {
		if len(line) == 0 {
			return false, nil
		}
		sseData.AddLine(string(line), processData)
		if processDataErr != nil {
			return true, processDataErr
		}
		return processDataDone, nil
	}

	flushData := func() (bool, error) {
		sseData.Flush(processData)
		if processDataErr != nil {
			return true, processDataErr
		}
		return processDataDone, nil
	}

	finalizePending := func() error {
		if imageCount > 0 {
			return nil
		}
		if len(pendingResults) > 0 {
			eventName := streamPrefix + ".completed"
			finalResults := append([]OpenAIResponsesImageResult(nil), pendingResults...)
			for i := range finalResults {
				MergeOpenAIResponsesImageMeta(&finalResults[i], streamMeta)
			}
			ReconcileOpenAIResponsesImageResultSizes(finalResults, nil)
			for _, img := range finalResults {
				key := OpenAIResponsesImageResultKey("", img)
				if _, exists := emitted[key]; exists {
					continue
				}
				payload := BuildOpenAIImagesStreamCompletedPayload(eventName, img, format, createdAt, nil, time.Now)
				emitted[key] = struct{}{}
				TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, eventName, payload)
			}
			imageCount = len(emitted)
			imageOutputSizes = OpenAIResponsesImageResultSizes(finalResults)
			return nil
		}

		streamErr := fmt.Errorf("stream disconnected before image generation completed")
		TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBody(streamErr.Error()))
		return streamErr
	}

	streamInterval := options.StreamInterval()
	keepaliveInterval := options.KeepaliveInterval()
	if streamInterval <= 0 && keepaliveInterval <= 0 {
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadBytes('\n')
			done, processErr := processLine(line)
			if processErr != nil {
				return usage, imageCount, imageOutputSizes, firstTokenMs, processErr
			}
			if done {
				return usage, imageCount, imageOutputSizes, firstTokenMs, nil
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				if done, processErr := flushData(); processErr != nil {
					return usage, imageCount, imageOutputSizes, firstTokenMs, processErr
				} else if done {
					return usage, imageCount, imageOutputSizes, firstTokenMs, nil
				}
				err = options.ClassifyReadError(err)
				return usage, imageCount, imageOutputSizes, firstTokenMs, err
			}
		}
		if done, processErr := flushData(); processErr != nil {
			return usage, imageCount, imageOutputSizes, firstTokenMs, processErr
		} else if done {
			return usage, imageCount, imageOutputSizes, firstTokenMs, nil
		}
		if err := finalizePending(); err != nil {
			return usage, imageCount, imageOutputSizes, firstTokenMs, err
		}
		return usage, imageCount, imageOutputSizes, firstTokenMs, nil
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
				if done, processErr := flushData(); processErr != nil {
					return usage, imageCount, imageOutputSizes, firstTokenMs, processErr
				} else if done {
					return usage, imageCount, imageOutputSizes, firstTokenMs, nil
				}
				if err := finalizePending(); err != nil {
					return usage, imageCount, imageOutputSizes, firstTokenMs, err
				}
				return usage, imageCount, imageOutputSizes, firstTokenMs, nil
			}
			if ev.err != nil {
				if done, processErr := flushData(); processErr != nil {
					return usage, imageCount, imageOutputSizes, firstTokenMs, processErr
				} else if done {
					return usage, imageCount, imageOutputSizes, firstTokenMs, nil
				}
				ev.err = options.ClassifyReadError(ev.err)
				return usage, imageCount, imageOutputSizes, firstTokenMs, ev.err
			}
			done, processErr := processLine(ev.line)
			if processErr != nil {
				return usage, imageCount, imageOutputSizes, firstTokenMs, processErr
			}
			if done {
				return usage, imageCount, imageOutputSizes, firstTokenMs, nil
			}
		case <-intervalCh:
			lastRead := time.Unix(0, atomic.LoadInt64(&lastReadAt))
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if clientDisconnected {
				return usage, imageCount, imageOutputSizes, firstTokenMs, fmt.Errorf("image stream incomplete after timeout")
			}
			options.Logf("[OpenAI] Images responses stream data interval timeout: interval=%s", streamInterval)
			TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBody(fmt.Sprintf("upstream image stream idle for %s", streamInterval)))
			return usage, imageCount, imageOutputSizes, firstTokenMs, fmt.Errorf("image stream data interval timeout")
		case <-keepaliveCh:
			if clientDisconnected || time.Since(lastDownstreamWriteAt) < keepaliveInterval {
				continue
			}
			if _, writeErr := io.WriteString(c.Writer, ":\n\n"); writeErr != nil {
				clientDisconnected = true
				options.Logf("[OpenAI] Images responses stream client disconnected during keepalive, continue draining upstream for billing")
				continue
			}
			flusher.Flush()
			lastDownstreamWriteAt = time.Now()
		}
	}
}

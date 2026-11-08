package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// images_apikey_response.go presents API-key Images responses to clients.
// Native JSON can retain optional URL backfill, while native SSE uses the
// shared frame reader for progressive delivery or buffered aggregation. Upstream
// observations are retained independently from successful downstream writes.
//
// Streaming delivery latches a client write failure and continues consuming
// upstream frames for settlement. Buffered SSE delivery waits for all requested
// completions and rejects errors or incomplete streams before writing JSON.
// Heartbeats and actual image events use distinct output markers so a waiting
// connection does not commit the gateway's image retry window. The executor
// owns the upstream response body and presents errors in the client format.
// Successful buffered GIF responses use verified dimensions for both client
// metadata and billing, while failed reads retain their original observations.

// ReadImagesNonStreaming reads an API-key image body, captures upstream usage
// and image dimensions, and writes the backfilled representation with the
// upstream status. ReadBody, Backfill, and ResponseHeaders are required for
// this path; the executor retains response-body ownership.
//
// Successful JSON is validated before accounting and output. Error envelopes
// and invalid image arrays return terminal errors with no completed-image count.
// Usage and output counts are extracted before Backfill can replace URL entries
// with encoded data. PreserveContentType allows a supplied upstream type to
// override the JSON default. Sink delivery uses OutputContext.Data, whose write
// outcome is not returned by this reader.
func ReadImagesNonStreaming(resp *http.Response, sink upstream.OutputSink, options ImageResponseOptions) (wire.ForwardUsage, int, []string, error) {
	body, err := options.ReadBody(resp.Body)
	if err != nil {
		return wire.ForwardUsage{}, 0, nil, err
	}
	if _, err := validateImagesJSONResponse(body); err != nil {
		return wire.ForwardUsage{}, 0, nil, err
	}
	// Observe accounting before Backfill changes the client representation, so URL
	// download outcomes cannot remove upstream-reported image usage or dimensions.
	usage, _ := wire.ExtractNativeImageUsage(body)
	imageCount := wire.CountOpenAIResponseImageOutputsFromJSONBytes(body)
	imageSizes := wire.CollectOpenAIResponseImageOutputSizesFromJSONBytes(body)
	body, usage.ImageFrameCount, err = ValidateGIFImagesResponse(body, options.ExpectedGIF, options.FetchImage)
	if err != nil {
		return usage, imageCount, imageSizes, err
	}
	if usage.ImageFrameCount > 0 {
		// GIF validation obtained complete bytes before delivery. Use their actual
		// dimensions for billing even when the upstream omitted its size metadata.
		imageSizes = wire.CollectOpenAIResponseImageOutputSizesFromJSONBytes(body)
	}
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

// ReadImagesStreaming delivers native Images frames through the shared SSE reader.
// Client write failures stop delivery while reading continues for accounting.
// The executor owns response closure, error presentation, and the upstream context.
//
// Frame and heartbeat callbacks run synchronously on the reader's consumer
// goroutine. They share a disconnect latch and the timestamp of the last
// successful write, so heartbeats stop after departure and cannot race image
// delivery. Completed-image observations come from the reader even when delivery
// failed; the final DONE marker is sent only after successful stream validation.
// Committed streams receive one final error event on failure, while uncommitted
// responses leave HTTP status and JSON error presentation to the executor.
func ReadImagesStreaming(ctx context.Context, resp *http.Response, c *upstream.OutputContext, options ImageResponseOptions) (wire.ForwardUsage, int, []string, error) {
	options.ResponseHeaders(c.Writer.Header(), resp.Header)
	c.Writer.Header().Del("Content-Length")
	c.Status(resp.StatusCode)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	disconnected := false
	lastWrite := time.Now()
	emit := func(name string, body []byte) {
		// Latch failed downstream writes while the shared reader continues collecting
		// generated images and usage from the selected upstream attempt.
		TryWriteImagesStreamEvent(c, options, c.Writer, &disconnected, &lastWrite, name, body)
	}
	heartbeat := func() {
		if disconnected || (options.KeepaliveInterval != nil && time.Since(lastWrite) < options.KeepaliveInterval()) {
			return
		}
		n, err := io.WriteString(c.Writer, ":\n\n")
		if options.RecordKeepalive != nil {
			options.RecordKeepalive(n)
		}
		if err != nil {
			disconnected = true
			return
		}
		c.Writer.Flush()
		lastWrite = time.Now()
	}
	result, err := ReadImagesSSE(ctx, resp, options, emit, heartbeat)
	if err != nil && c.Writer.Written() {
		upstreamErr, ok := errors.AsType[*OpenAIImagesUpstreamError](err)
		if !ok {
			upstreamErr = imagesProtocolError("Upstream image stream was interrupted")
		}
		// Once SSE is committed, the native reader owns its final error frame.
		// The generic HTTP error presenter refuses writes after actual image content.
		upstreamErr.UpstreamRequestID = resp.Header.Get("x-request-id")
		if options.ObserveError != nil {
			options.ObserveError(resp.StatusCode, upstreamErr.Message, "")
		}
		if options.ObserveStreamError != nil {
			options.ObserveStreamError(upstreamErr)
		}
		c.NextEvent(false, true)
		TryWriteImagesStreamEvent(c, options, c.Writer, &disconnected, &lastWrite, "error", BuildOpenAIImagesStreamErrorBodyFromUpstream(upstreamErr))
	}
	if err == nil && !disconnected {
		c.NextEvent(false, false)
		if _, writeErr := io.WriteString(c.Writer, "data: [DONE]\n\n"); writeErr == nil {
			c.Writer.Flush()
		}
	}
	result.Usage.ImageFrameCount = result.Counter.FrameCount()
	return result.Usage, result.Counter.Count(), result.Counter.Sizes(), err
}

// ReadImagesSSEToJSON aggregates native Images events for a buffered client.
// Failed streams retain completed-image and usage observations without delivering
// a partial JSON success. The caller owns error presentation and response closure.
//
// Successful GIF normalization supplies dimensions from returned bytes, replacing
// the counter's original upstream size labels for billing. Static responses and
// failures keep those original observations. The verified JSON is delivered before
// returning the same size evidence used by the completion recorder.
func ReadImagesSSEToJSON(ctx context.Context, resp *http.Response, sink upstream.OutputSink, options ImageResponseOptions, responseFormat string) (wire.ForwardUsage, int, []string, error) {
	body, result, err := ReadImagesBufferedResponse(ctx, resp, options, responseFormat)
	imageSizes := result.Counter.Sizes()
	if err == nil {
		// The counter observed raw events before GIF validation corrected dimensions.
		// Read the verified document so delivered metadata and billing use one canvas.
		if result.Usage.ImageFrameCount > 0 {
			imageSizes = wire.CollectOpenAIResponseImageOutputSizesFromJSONBytes(body)
		}
		c := upstream.NewOutputContext(sink)
		options.ResponseHeaders(c.Writer.Header(), resp.Header)
		c.Writer.Header().Del("Content-Length")
		c.Header("Content-Type", "application/json")
		c.Data(http.StatusOK, "application/json", body)
	}
	return result.Usage, result.Counter.Count(), imageSizes, err
}

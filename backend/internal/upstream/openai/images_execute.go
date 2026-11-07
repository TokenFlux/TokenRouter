package openai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// images_execute.go executes image attempts and records output commitment,
// served images, usage, and delivery failures.
// An image attempt can commit HTTP output or emit progressive image data before
// a completed image is counted. Execution records those observations
// independently, allowing a failed OAuth stream to report partial served output
// while ObservedImages remains zero.
//
// The prepared request owns transport context, and optional admission stays
// held through response reading and cleanup. HTTP failures close the original
// body before replaying redacted bytes into health handling. Successful
// responses select OAuth readers or native JSON/SSE readers independently of
// the client mode. Native streams can aggregate into JSON, while standard
// Images JSON documents can produce completed SSE events. A shared waiting
// heartbeat covers response headers and buffered JSON reads, then stops before output
// begins. Invalid JSON ends the attempt before requested-count substitution.
// A synchronous sink wrapper retains commitment, semantic, and write-failure
// observations across those branches,
// while OAuth response failure policy also considers whether any completed
// image was observed before the error.

// ImagesTarget holds the prepared request, response policy, and image execution
// callbacks.
type ImagesTarget struct {
	ProviderID int64 // selected image provider identifier
	OAuth      bool  // OAuth image conversion selection
	// Model is the selected upstream model, ResponseFormat is the client image
	// encoding choice, and StreamPrefix names the image event family used by
	// OAuth conversion. They describe one prepared attempt across its readers.
	Model, ResponseFormat, StreamPrefix string
	// StartedAt supplies the attempt duration origin; a zero timestamp selects the
	// execution start, and response processing records duration before body
	// cleanup.
	StartedAt time.Time
	// Request contains prepared authentication and owns the transport context;
	// callers redact diagnostics and keep its body usable for this single exchange.
	Request *http.Request        `json:"-"`
	Options ImageResponseOptions // prepared image response configuration
	// Enter optionally admits activity and returns its release callback; Execute
	// defers release through response reading, result observation, and body
	// cleanup.
	Enter           func() (func(), error)
	Do              func(*http.Request) (*http.Response, error) // required HTTP exchange callback
	TransportError  func(error) error                           // required transport failure translator
	ReadErrorBody   func(*http.Response) []byte                 // required HTTP error reader
	RedactErrorBody func([]byte) []byte                         // required captured error redactor
	HTTPError       func(*http.Response, []byte) error          // required HTTP status handler
	ResponseError   func(*http.Response, int, error) error      // required OAuth response policy
}

// ImagesExecutor sends prepared image attempts and records response output and
// usage.
type ImagesExecutor struct{}

// imagesObservedSink records commitment and attempted semantic output through a
// synchronous sink.
type imagesObservedSink struct {
	upstream.OutputSink // wrapped synchronous image output sink
	// committed records accepted headers, retryCommitted records retry-commit
	// event flags, semantic records attempted content, and failed records sink
	// write failure. Emit retains event observations even if delivery fails.
	committed, retryCommitted, semantic, failed bool
}

// TargetID returns the selected image provider identifier or zero for nil
// targets. Diagnostic formatting can therefore describe missing preparation
// before Execute rejects an unavailable request target.
func (t *ImagesTarget) TargetID() int64 {
	if t == nil {
		return 0
	}
	return t.ProviderID
}

// String formats the image provider identifier while omitting request
// credentials.
func (t *ImagesTarget) String() string {
	return fmt.Sprintf("openai images target provider=%d", t.TargetID())
}

// GoString returns the redacted image target label for Go-syntax formatting.
func (t *ImagesTarget) GoString() string { return t.String() }

// Execute sends one prepared image attempt and reports usage, completed images,
// and output observations. The target supplies required transport/error
// callbacks and response options; Enter is optional, and a zero StartedAt
// selects the current time. The prepared request owns the context used by Do.
//
// HTTP error handling closes the original body before installing a redacted
// replay reader. Successful responses select the appropriate streaming or
// buffered reader, while the sink wrapper records HTTP commitment, retry
// commitment, attempted semantic output, and write failure separately. Served
// can therefore be true for progressive semantic output with no completed
// image. Waiting heartbeats cover headers and buffered JSON reads, then stop
// before any sink access. OAuth Responses errors before a completed streaming
// image or in nonstream mode invoke ResponseError; Images JSON failures use
// terminal image errors after validation. Canceled or expired reads retain their
// cause for outer stop handling.
func (ImagesExecutor) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (result upstream.AttemptResult, failure error) {
	defer func() { result.Finish(ctx, failure) }()
	t, ok := input.Target.(*ImagesTarget)
	if !ok || t == nil || t.Request == nil {
		return result, errors.New("openai images target is not configured")
	}
	if input.Protocol != protocol.ProtocolImagesGenerations && input.Protocol != protocol.ProtocolImagesEdits {
		return result, errors.New("unsupported images protocol")
	}
	if t.Enter != nil {
		done, err := t.Enter()
		if err != nil {
			return result, err
		}
		defer done()
	}
	started := t.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	defer func() {
		if result.Duration == 0 {
			result.Duration = time.Since(started)
		}
		result.Cancelled = errors.Is(failure, context.Canceled) || errors.Is(failure, context.DeadlineExceeded)
		if failure != nil {
			result.FailureClass = "upstream"
		}
	}()
	stopKeepalive := func() {}
	if input.Stream && t.Options.StartKeepalive != nil {
		stopKeepalive = t.Options.StartKeepalive()
	}
	// Keep the waiting writer alive through buffered bodies, including HTTP
	// failures. Every presentation path stops it before using the captured sink.
	defer stopKeepalive()
	resp, err := t.Do(t.Request)
	if err != nil {
		stopKeepalive()
		return result, t.TransportError(err)
	}
	result.CaptureRequestAttempt(resp)
	// Close the body visible after response policy returns, before admission
	// release; HTTP failures replace the already closed transport body with redacted replay bytes.
	defer func() { _ = resp.Body.Close() }()
	// Record exchange duration before body cleanup and admission release, so
	// connection closing time is excluded from the measured image response processing.
	defer func() { result.Duration = time.Since(started) }()
	if resp.StatusCode >= 400 {
		body := t.ReadErrorBody(resp)
		stopKeepalive()
		// Release the failed transport body before health handling consumes replay
		// bytes, and redact that replay so its diagnostics receive no provider credentials.
		_ = resp.Body.Close()
		body = t.RedactErrorBody(body)
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return result, t.HTTPError(resp, body)
	}
	mediaType, _, mediaErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	streamJSON := input.Stream && (mediaErr != nil || mediaType != "text/event-stream")
	var jsonBody []byte
	if streamJSON {
		jsonBody, err = t.Options.ReadBody(resp.Body)
	}
	// The sink was captured before heartbeat wrapping. Stop and restore the
	// waiting writer before sink initialization reads headers or presents errors.
	stopKeepalive()
	// Cancellation and deadlines stop execution rather than invalidate JSON.
	// Keep their error chain available to diagnostics and outer cancellation policy.
	if err != nil &&
		!errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded) {
		err = imagesProtocolError("Failed to read upstream image JSON response: " + logredact.SanitizeUpstreamQueries(err.Error()))
	}
	observed := &imagesObservedSink{OutputSink: sink}
	sink = observed
	defer func() {
		result.HTTPCommitted = observed.committed
		result.RetryCommitted = observed.retryCommitted
		result.Served = result.Served || observed.semantic
		result.ClientDisconnect = observed.failed
	}()
	var usage wire.ForwardUsage
	var output *upstream.OutputContext
	result.RequestID = resp.Header.Get("x-request-id")
	result.UpstreamHeaders = resp.Header
	result.Model = input.ResponseModel
	result.UpstreamModel = t.Model
	result.Stream = input.Stream
	before := 0
	if t.OAuth {
		before = t.Options.AdjustedWrittenSize()
	}
	t.Options.RequireBase64 = !t.OAuth
	t.Options.EventPrefix = "image_generation"
	if input.Protocol == protocol.ProtocolImagesEdits {
		t.Options.EventPrefix = "image_edit"
	}
	if input.Stream {
		output = upstream.NewOutputContext(sink)
		if streamJSON {
			eventName := "image_generation.completed"
			if input.Protocol == protocol.ProtocolImagesEdits {
				eventName = "image_edit.completed"
			}
			if err == nil {
				usage, result.ObservedImages, result.ImageOutputSizes, err = WriteImagesJSONStreaming(jsonBody, resp, output, t.Options, eventName)
			}
			if imageErr, ok := errors.AsType[*OpenAIImagesUpstreamError](err); ok {
				imageErr.UpstreamRequestID = result.RequestID
				if t.Options.ObserveError != nil {
					t.Options.ObserveError(resp.StatusCode, imageErr.Message, "")
				}
				if t.Options.WriteHTTPError != nil {
					t.Options.WriteHTTPError(imageErr)
				}
			}
		} else if t.OAuth {
			usage, result.ObservedImages, result.ImageOutputSizes, err = ReadImagesOAuthStreaming(resp, output, t.Options, started, t.ResponseFormat, t.StreamPrefix, input.ResponseModel)
		} else {
			usage, result.ObservedImages, result.ImageOutputSizes, err = ReadImagesStreaming(ctx, resp, output, t.Options)
		}
	} else if t.OAuth {
		usage, result.ObservedImages, result.ImageOutputSizes, err = ReadImagesOAuthNonStreaming(resp, sink, t.Options, t.ResponseFormat, input.ResponseModel)
	} else if mediaErr == nil && mediaType == "text/event-stream" {
		usage, result.ObservedImages, result.ImageOutputSizes, err = ReadImagesSSEToJSON(ctx, resp, sink, t.Options, t.ResponseFormat)
	} else {
		usage, result.ObservedImages, result.ImageOutputSizes, err = ReadImagesNonStreaming(resp, sink, t.Options)
	}
	if !t.OAuth && !streamJSON {
		if imageErr, ok := errors.AsType[*OpenAIImagesUpstreamError](err); ok {
			imageErr.UpstreamRequestID = result.RequestID
			if t.Options.ObserveError != nil {
				t.Options.ObserveError(resp.StatusCode, imageErr.Message, "")
			}
			if t.Options.WriteHTTPError != nil {
				t.Options.WriteHTTPError(imageErr)
			}
		}
	}
	result.Usage = upstream.TokenUsage{
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		ImageOutputTokens:        usage.ImageOutputTokens,
	}
	result.ImageFrameCount = usage.ImageFrameCount
	result.ImageInputTokens = usage.ImageInputTokens
	result.ImageCacheReadTokens = usage.ImageCacheReadTokens
	result.HasUsage = result.Usage.HasObservedTokens() || usage.ImageInputTokens > 0
	result.Served = result.ObservedImages > 0
	if output != nil {
		result.HTTPCommitted = output.Writer.Written()
	}
	// Responses failure policy may retry before image output. Images JSON failures
	// remain terminal because a successful HTTP exchange may already have generated images.
	if err != nil && !streamJSON && t.OAuth && result.ObservedImages <= 0 {
		err = t.ResponseError(resp, before, err)
	}
	return result, err
}

// InitialOutput reads existing output state when the wrapped sink supports it.
// It records the supplied committed flag; sinks without that optional interface
// return an empty head and retain ordinary output initialization.
func (s *imagesObservedSink) InitialOutput() upstream.OutputHead {
	if source, ok := s.OutputSink.(interface {
		// InitialOutput reports the output head already established by this sink.
		// The wrapper reads its committed flag before further image response output
		// begins for this attempt.
		InitialOutput() upstream.OutputHead
	}); ok {
		head := source.InitialOutput()
		s.committed = head.Committed
		return head
	}
	return upstream.OutputHead{}
}

// Begin delegates response commitment and records success or write failure. A
// successful call sets committed, while a failure marks the client-disconnect
// observation for the enclosing attempt result.
func (s *imagesObservedSink) Begin(head upstream.OutputHead) error {
	err := s.OutputSink.Begin(head)
	if err == nil {
		s.committed = true
	} else {
		s.failed = true
	}
	return err
}

// Emit records retry and semantic event flags before delegating delivery. A
// failed write sets failed but retains those attempted-output flags, so callers
// can distinguish completed images from output attempts and downstream
// disconnection.
func (s *imagesObservedSink) Emit(event upstream.OutputEvent) error {
	s.retryCommitted = s.retryCommitted || event.CommitForRetry
	s.semantic = s.semantic || event.Semantic
	err := s.OutputSink.Emit(event)
	if err != nil {
		s.failed = true
	}
	return err
}

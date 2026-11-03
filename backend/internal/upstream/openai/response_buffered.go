package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// response_buffered.go reads buffered Responses streams through terminal
// output while accumulating usage and conversion state.
// Buffered compatibility reads combine SSE framing, accumulated output, and
// final response usage before a nonstreaming caller constructs its result. A
// terminal's supplied categories take precedence over accumulated usage, including
// explicitly zero snapshots, while a bare failure can synthesize a failed response
// from its error fields.
//
// A scanner goroutine feeds line events to the reader so interval timing can
// proceed while body reads wait. Any scanned line renews the configured
// interval, and timeout closes the body to unblock transport reading. Other
// returns close a stop channel but leave body ownership with the caller, which
// must close the response to release a potentially blocked scanner. Malformed
// completed frames are skipped; EOF processes a final unterminated frame before
// returning the observations already collected.

// CompatBufferedOptions configures framing, interval timing, and synchronous
// buffered-response observations.
type CompatBufferedOptions struct {
	MaxLineSize int // scanner growth byte limit
	// StreamInterval is read once per buffered exchange; nonpositive durations
	// disable interval timing, while scanned lines renew the positive budget.
	StreamInterval func() time.Duration
	// RestoreToolNames rewrites a complete frame payload before JSON decoding;
	// Observe and the accumulator consume its returned bytes for the same event.
	RestoreToolNames func([]byte) []byte
	Observe          func([]byte, string)               // required restored event observer
	Log              func(string, error, time.Duration) // required read and parse logger
}

// CompatBufferedReadError retains a body-read cause for endpoint-specific
// recovery decisions.
type CompatBufferedReadError struct {
	cause error // underlying response read failure
}

// NewCompatSSEScanner returns a line scanner with a sixty-four-KiB initial
// buffer and maxLineSize growth limit. The initial buffer can already admit
// larger lines than a smaller supplied maximum; callers own the reader
// lifetime.
func NewCompatSSEScanner(r io.Reader, maxLineSize int) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	// Start with storage sized for ordinary SSE lines before scanner growth.
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	return scanner
}

// IsCompatResponsesTerminalEvent reports whether the trimmed name ends a
// Responses exchange.
func IsCompatResponsesTerminalEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.completed", "response.done", "response.incomplete", "response.failed", "response.cancelled", "response.canceled", "error":
		return true
	default:
		return false
	}
}

// IsCompatDoneSentinelLine reports whether one SSE data line carries the DONE
// marker.
func IsCompatDoneSentinelLine(line string) bool {
	payload, ok := wire.ExtractSSEDataLine(line)
	return ok && strings.TrimSpace(payload) == "[DONE]"
}

// Error returns the message of this buffered read failure cause.
func (e *CompatBufferedReadError) Error() string { return e.cause.Error() }

// Unwrap returns the buffered read cause for typed error inspection.
func (e *CompatBufferedReadError) Unwrap() error { return e.cause }

// CompatTerminalResponse prefers the event response or synthesizes a failed
// response for bare error events. Nil events and other event types return nil;
// missing error text receives the fixed upstream-failure message.
func CompatTerminalResponse(event *wire.ResponsesStreamEvent, payload []byte) *wire.ResponsesResponse {
	if event == nil {
		return nil
	}
	if event.Response != nil {
		return event.Response
	}
	switch strings.TrimSpace(event.Type) {
	case "response.failed", "error":
		message := ExtractOpenAISSEErrorMessage(payload)
		if message == "" {
			message = "Upstream response failed"
		}
		return &wire.ResponsesResponse{
			Status: "failed",
			Error:  &wire.ResponsesError{Code: event.Code, Message: message},
		}
	default:
		return nil
	}
}

// ReadCompatBufferedTerminal reads SSE until a terminal response, DONE, EOF,
// read failure, or interval timeout. Required options restore tool names before
// observation and accumulation, read the interval once, and log failures. The
// caller owns resp.Body and closes it after return; timeout closes it early.
//
// Line arrival renews the interval even when a frame is incomplete. A scanner
// goroutine reports lines and errors through a bounded channel, while a
// deferred stop signal ends its next send; a blocked body read still requires
// body closure and is not joined here. Terminal response categories override
// supplied event categories while omitted fields retain prior usage. EOF attempts its
// final frame, and malformed frames leave previously accumulated output and
// usage available.
func ReadCompatBufferedTerminal(resp *http.Response, options CompatBufferedOptions) (*wire.ResponsesResponse, wire.ForwardUsage, *bridge.BufferedResponseAccumulator, error) {
	acc := bridge.NewBufferedResponseAccumulator()
	var usage wire.ForwardUsage
	var usageAccumulator wire.UsageAccumulator
	if resp == nil || resp.Body == nil {
		return nil, usage, acc, errors.New("upstream response body is nil")
	}

	scanner := NewCompatSSEScanner(resp.Body, options.MaxLineSize)

	streamInterval := options.StreamInterval()
	var timeoutCh <-chan time.Time
	var timeoutTimer *time.Timer
	resetTimeout := func() {
		if streamInterval <= 0 {
			return
		}
		if timeoutTimer == nil {
			timeoutTimer = time.NewTimer(streamInterval)
			timeoutCh = timeoutTimer.C
			return
		}
		// Drain an available old tick after failed Stop so reset or cleanup
		// does not let a previous interval expiry become the next wait result.
		if !timeoutTimer.Stop() {
			select {
			case <-timeoutTimer.C:
			default:
			}
		}
		timeoutTimer.Reset(streamInterval)
	}
	stopTimeout := func() {
		if timeoutTimer == nil {
			return
		}
		// Drain an available old tick after failed Stop so reset or cleanup
		// does not let a previous interval expiry become the next wait result.
		if !timeoutTimer.Stop() {
			select {
			case <-timeoutTimer.C:
			default:
			}
		}
	}
	resetTimeout()
	defer stopTimeout()

	// scanEvent carries one scanned line or a read failure to interval selection.
	type scanEvent struct {
		line string // scanned SSE line text
		err  error  // scanner body read failure
	}
	events := make(chan scanEvent, 16)
	done := make(chan struct{})
	// Scanner reading progresses independently of interval selection. The done
	// channel stops sends after return, while response closure unblocks pending transport reads.
	go func() {
		defer close(events)
		for scanner.Scan() {
			select {
			case events <- scanEvent{line: scanner.Text()}:
			case <-done:
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case events <- scanEvent{err: err}:
			case <-done:
			}
		}
	}()
	defer close(done)

	processFrame := func(frame wire.OpenAICompatSSEFrame) (*wire.ResponsesResponse, error) {
		payload := wire.OpenAICompatPayloadWithEventType(frame.Data, frame.EventType)
		payload = string(options.RestoreToolNames([]byte(payload)))
		var event wire.ResponsesStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return nil, err
		}
		options.Observe([]byte(payload), event.Type)
		wire.ParseSSEUsageBytesWithType([]byte(payload), event.Type, &usage)
		if snapshot := usageAccumulator.ObserveResponsesEvent([]byte(payload), &event); snapshot != nil {
			usage = usageAccumulator.ForwardUsage()
		}
		acc.ProcessEvent(&event)
		if response := CompatTerminalResponse(&event, []byte(payload)); IsCompatResponsesTerminalEvent(event.Type) && response != nil {
			// The accumulator has already resolved terminal precedence. Fill a missing
			// response usage without replacing accounting-only image buckets from typed data.
			if response.Usage == nil {
				response.Usage = event.Usage
			}
			return response, nil
		}
		return nil, nil
	}

	var parser wire.OpenAICompatSSEFrameParser
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				if frame, ok := parser.Finish(); ok {
					// EOF flushes an unterminated frame once; parse failure retains the
					// accumulated observations and has no additional per-line logging at this point.
					if response, _ := processFrame(frame); response != nil {
						return response, usage, acc, nil
					}
				}
				return nil, usage, acc, nil
			}
			resetTimeout()
			if ev.err != nil {
				if !errors.Is(ev.err, context.Canceled) && !errors.Is(ev.err, context.DeadlineExceeded) {
					options.Log("read error", ev.err, 0)
				}
				return nil, usage, acc, &CompatBufferedReadError{cause: ev.err}
			}

			if IsCompatDoneSentinelLine(ev.line) {
				return nil, usage, acc, nil
			}
			frame, ok := parser.AddLine(ev.line)
			if !ok {
				continue
			}
			response, err := processFrame(frame)
			if err != nil {
				// A malformed complete frame is logged and skipped so other usable
				// events can still contribute output, usage, or a later terminal response.
				options.Log("failed to parse event", err, 0)
				continue
			}
			if response != nil {
				return response, usage, acc, nil
			}

		case <-timeoutCh:
			// Closing the body releases a scanner blocked in Read, while the
			// deferred done signal releases any pending line send after this timeout result.
			_ = resp.Body.Close()
			options.Log("data interval timeout", nil, streamInterval)
			return nil, usage, acc, fmt.Errorf("stream data interval timeout")
		}
	}
}

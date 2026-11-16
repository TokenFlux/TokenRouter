package forward

import (
	"context"
	"fmt"
	"time"

	"github.com/tidwall/gjson"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// anthropic_passthrough.go forwards API-key requests and returns model and
// usage evidence from the selected provider. Empty text blocks and unsupported
// search history are removed before the shared parsed body is replaced.
// Credential kind is checked independently of the caller's forwarding flags, so
// a successful acquisition still needs to identify an apikey credential.
//
// Response hooks decide error policy before upstream output continues. They
// retain response metadata and the latest wire body for final usage evidence.
// Stream errors can return positive observed usage with the error when no
// failover signal is requested; the exchange executor owns response reading and
// closure, while this wrapper releases its attempt activity after all handling.

// APIKeyPassthrough executes a filtered request with an exact apikey credential
// and returns its usage observations or early error outcome. Successful Begin
// supplies a usable release function, and ExecutePassthrough invokes response
// and stream hooks serially before returning.
//
// Body rewrites update an optional shared parsed request before exchange.
// Status-qualified failure paths classify available error bytes and either
// delegate generic display or return a failover signal. Other stream errors
// can retain partial positive usage, headers, and timing alongside the error;
// that result requires captured response metadata and shares its header and
// timing pointers with the producer.
func APIKeyPassthrough(ctx context.Context, p PassthroughPorts, in MessageInput, input APIKeyInput) (*Result, error) {
	done, err := p.Begin()
	if err != nil {
		return nil, err
	}
	// Keep the attempt active through credential acquisition and response
	// handling. The deferred release also runs when an early policy or
	// preparation error returns before the exchange completes.
	defer done()
	err = p.Credential(ctx)
	if err != nil {
		return nil, err
	}
	if tokenType := p.TokenKind(); tokenType != "apikey" {
		return nil, fmt.Errorf("anthropic api key passthrough requires apikey token, got: %s", tokenType)
	}

	p.ResolveProxy()

	p.Log(fmt.Sprintf("[Anthropic auto passthrough] Selected API key passthrough: provider=%d name=%s model=%s stream=%v",
		in.ProviderID, in.ProviderName, input.RequestModel, input.RequestStream))

	p.MarkPassthrough()

	input.Body = protocolanthropic.StripEmptyTextBlocks(input.Body)
	// Search history eligibility follows the already mapped provider model.
	// Remove unsupported server-tool blocks before the parsed body is republished
	// so subsequent request views use this forwarding representation.
	input.Body = p.FilterSearchHistory(input.Body, input.RequestModel)
	// Rewrite ephemeral cache markers for eligible Claude passthrough requests
	// before republishing the shared parsed body so downstream views match wire bytes.
	if p.InjectTTL(ctx, input.RequestModel) {
		input.Body = p.CacheTTL(input.Body)
	}
	if input.Parsed != nil {
		// Reparse the filtered body in the shared request state. Later body views
		// and usage-hash extraction then read these bytes rather than the input
		// representation retained before filtering.
		if err := input.Parsed.ReplaceBody(input.Body); err != nil {
			return nil, err
		}
	}

	var resp *ExchangeResponse
	lastWireBody := input.Body
	var earlyResult *Result
	stopped := false
	var streamResult *StreamOutcome
	before := func(ctx context.Context, response *ExchangeResponse, wire []byte) (bool, error) {
		resp = response
		lastWireBody = wire
		early := func(result *Result, err error) (bool, error) { earlyResult = result; return true, err }
		if resp.StatusCode >= 400 && ShouldRetry(in.OAuth, resp.StatusCode) {
			if ShouldFailover(resp.StatusCode) {
				// Retain available error bytes even after a read failure, then reset
				// the consumed reader for retry health policy. The captured slice
				// stays available for this branch's observations and retry signal.
				respBody, _ := p.ReadErrorBody()
				p.ResetErrorBody(respBody)

				// Keep the routine error excerpt bounded independently of optional detail.
				p.Log(fmt.Sprintf("[Anthropic Passthrough] Upstream error (retry exhausted, failover): Provider=%d(%s) Status=%d RequestID=%s Body=%s",
					in.ProviderID, in.ProviderName, resp.StatusCode, resp.RequestID, p.Truncate(string(respBody), 1000)))

				decision := p.Health(ctx, "retry", resp.StatusCode, resp.Headers, respBody, input.RequestModel)
				if decision.Generic {
					return early(p.HandleError(ctx, input.RequestModel, false))
				}
				p.Observe(Notice{
					Platform:           in.Platform,
					ProviderID:         in.ProviderID,
					ProviderName:       in.ProviderName,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.RequestID,
					Passthrough:        true,
					Kind:               "retry_exhausted_failover",
					Message:            upstream.ExtractErrorMessage(respBody),
					Detail: func() string {
						if in.LogErrorBody {
							return p.Truncate(string(respBody), in.LogErrorBodyMaxBytes)
						}
						return ""
					}(),
				})
				return true, p.FailoverError(resp.StatusCode, respBody, decision.RetrySameProvider)
			}
			return early(p.HandleError(ctx, input.RequestModel, true))
		}

		if resp.StatusCode >= 400 && ShouldFailover(resp.StatusCode) {
			// Classification can use partial response evidence. Reset the reader
			// before policy rereads it, retaining the captured slice separately
			// for the resulting failover observation and signal.
			respBody, _ := p.ReadErrorBody()
			p.ResetErrorBody(respBody)

			// Keep the routine error excerpt bounded independently of optional detail.
			p.Log(fmt.Sprintf("[Anthropic Passthrough] Upstream error (failover): Provider=%d(%s) Status=%d RequestID=%s Body=%s",
				in.ProviderID, in.ProviderName, resp.StatusCode, resp.RequestID, p.Truncate(string(respBody), 1000)))

			decision := p.Health(ctx, "failover", resp.StatusCode, resp.Headers, respBody, input.RequestModel)
			if decision.Generic {
				return early(p.HandleError(ctx, input.RequestModel, false))
			}
			p.Observe(Notice{
				Platform:           in.Platform,
				ProviderID:         in.ProviderID,
				ProviderName:       in.ProviderName,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.RequestID,
				Passthrough:        true,
				Kind:               "failover",
				Message:            upstream.ExtractErrorMessage(respBody),
				Detail: func() string {
					if in.LogErrorBody {
						return p.Truncate(string(respBody), in.LogErrorBodyMaxBytes)
					}
					return ""
				}(),
			})
			return true, p.FailoverError(resp.StatusCode, respBody, decision.RetrySameProvider)
		}

		if resp.StatusCode >= 400 {
			return early(p.HandleError(ctx, input.RequestModel, false))
		}

		return false, nil
	}
	attempt, err := p.ExecutePassthrough(ctx, &input, MessageHooks{
		Before: func(ctx context.Context, response *ExchangeResponse, wire []byte) (bool, error) {
			var err error
			stopped, err = before(ctx, response, wire)
			return stopped, err
		},
		Wire: func(wire []byte) { lastWireBody = wire },
		Stream: func(result *StreamOutcome) {
			if result != nil {
				streamResult = result
			}
		},
	})

	if stopped {
		return earlyResult, err
	}
	if err != nil {
		if input.RequestStream {
			requestSpeed := gjson.GetBytes(lastWireBody, "speed").String()
			// A non-failover stream error can still carry positive observed usage.
			// Preserve that outcome alongside its error; failover signals leave
			// completion to the later provider attempt.
			if partial := PartialUsage(resp, streamResult, input.OriginalModel, input.RequestModel, input.StartTime, requestSpeed, p.IsFailover(err)); partial != nil {
				return partial, err
			}
		}
		return nil, err
	}
	usage := &attempt.Usage

	clientDisconnect := attempt.ClientDisconnect
	return &Result{
		RequestID:                   resp.RequestID,
		UpstreamHeaders:             resp.Headers,
		Usage:                       *usage,
		Model:                       input.OriginalModel,
		UpstreamModel:               input.RequestModel,
		UpstreamResponseServiceTier: p.ServiceTier(),
		UpstreamResponseModel:       attempt.UpstreamResponseModel,
		Stream:                      input.RequestStream,
		Duration:                    time.Since(input.StartTime),
		Timings:                     nil,
		ClientDisconnect:            clientDisconnect,
	}, nil
}

// 兼容读取器保留原逐行缓冲、超时和 usage 优先级，不决定是否切换账号。
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

// CompatBufferedOptions 只提供本次技术预算与原观察端口。
type CompatBufferedOptions struct {
	MaxLineSize      int
	StreamInterval   func() time.Duration
	RestoreToolNames func([]byte) []byte
	Observe          func([]byte, string)
	Log              func(string, error, time.Duration)
}

// NewCompatSSEScanner 保留独立 64 KiB 起始缓冲，不借此合并旧扫描池策略。
func NewCompatSSEScanner(r io.Reader, maxLineSize int) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	return scanner
}
func IsCompatResponsesTerminalEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.completed", "response.done", "response.incomplete", "response.failed", "response.cancelled", "response.canceled", "error":
		return true
	default:
		return false
	}
}

func IsCompatDoneSentinelLine(line string) bool {
	payload, ok := wire.ExtractSSEDataLine(line)
	return ok && strings.TrimSpace(payload) == "[DONE]"
}

// CompatBufferedReadError 只标记响应体读取阶段的错误；具体端点自行
// 决定是否允许重放，避免共享读取器扩大故障转移范围。
type CompatBufferedReadError struct {
	cause error
}

func (e *CompatBufferedReadError) Error() string { return e.cause.Error() }

func (e *CompatBufferedReadError) Unwrap() error { return e.cause }

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

func ReadCompatBufferedTerminal(resp *http.Response, options CompatBufferedOptions) (*wire.ResponsesResponse, wire.ForwardUsage, *bridge.BufferedResponseAccumulator, error) {
	acc := bridge.NewBufferedResponseAccumulator()
	var usage wire.ForwardUsage
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
		if !timeoutTimer.Stop() {
			select {
			case <-timeoutTimer.C:
			default:
			}
		}
	}
	resetTimeout()
	defer stopTimeout()

	type scanEvent struct {
		line string
		err  error
	}
	events := make(chan scanEvent, 16)
	done := make(chan struct{})
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

	var parser wire.OpenAICompatSSEFrameParser
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				if frame, ok := parser.Finish(); ok {
					payload := wire.OpenAICompatPayloadWithEventType(frame.Data, frame.EventType)
					payload = string(options.RestoreToolNames([]byte(payload)))
					var event wire.ResponsesStreamEvent
					if err := json.Unmarshal([]byte(payload), &event); err == nil {
						options.Observe([]byte(payload), event.Type)
						wire.ParseSSEUsageBytesWithType([]byte(payload), event.Type, &usage)
						acc.ProcessEvent(&event)
						if response := CompatTerminalResponse(&event, []byte(payload)); IsCompatResponsesTerminalEvent(event.Type) && response != nil {
							if event.Usage != nil {
								usage = wire.CopyForwardUsage(event.Usage)
								if response.Usage == nil {
									response.Usage = event.Usage
								}
							}
							if response.Usage != nil {
								usage = wire.CopyForwardUsage(response.Usage)
							}
							return response, usage, acc, nil
						}
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
			payload := wire.OpenAICompatPayloadWithEventType(frame.Data, frame.EventType)
			payload = string(options.RestoreToolNames([]byte(payload)))

			var event wire.ResponsesStreamEvent
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				options.Log("failed to parse event", err, 0)
				continue
			}
			options.Observe([]byte(payload), event.Type)
			wire.ParseSSEUsageBytesWithType([]byte(payload), event.Type, &usage)

			acc.ProcessEvent(&event)

			if response := CompatTerminalResponse(&event, []byte(payload)); IsCompatResponsesTerminalEvent(event.Type) && response != nil {
				if event.Usage != nil {
					usage = wire.CopyForwardUsage(event.Usage)
					if response.Usage == nil {
						response.Usage = event.Usage
					}
				}
				if response.Usage != nil {
					usage = wire.CopyForwardUsage(response.Usage)
				}
				return response, usage, acc, nil
			}

		case <-timeoutCh:
			_ = resp.Body.Close()
			options.Log("data interval timeout", nil, streamInterval)
			return nil, usage, acc, fmt.Errorf("stream data interval timeout")
		}
	}
}

package forward

import (
	"context"
	"fmt"
	"time"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
)

// APIKeyPassthrough 保留已选账号的响应判断与部分用量；交换和原生流仍由 upstream 唯一实现。
func APIKeyPassthrough(ctx context.Context, p PassthroughPorts, in MessageInput, input APIKeyInput) (*Result, error) {
	done, err := p.Begin()
	if err != nil {
		return nil, err
	}
	defer done()
	err = p.Credential(ctx)
	if err != nil {
		return nil, err
	}
	if tokenType := p.TokenKind(); tokenType != "apikey" {
		return nil, fmt.Errorf("anthropic api key passthrough requires apikey token, got: %s", tokenType)
	}

	p.ResolveProxy()

	p.Log(fmt.Sprintf("[Anthropic 自动透传] 命中 API Key 透传分支: account=%d name=%s model=%s stream=%v",
		in.AccountID, in.AccountName, input.RequestModel, input.RequestStream))

	p.MarkPassthrough()

	input.Body = protocolanthropic.StripEmptyTextBlocks(input.Body)
	// which reject server_tool_use with 400). input.RequestModel 已是映射后的模型 ID。
	input.Body = p.FilterSearchHistory(input.Body, input.RequestModel)
	if input.Parsed != nil {
		// 透传分支也会改写实际 wire body，成功 usage hash 依赖这里同步当前 body。
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
				respBody, _ := p.ReadErrorBody()
				p.ResetErrorBody(respBody)

				p.Log(fmt.Sprintf("[Anthropic Passthrough] Upstream error (retry exhausted, failover): Account=%d(%s) Status=%d RequestID=%s Body=%s",
					in.AccountID, in.AccountName, resp.StatusCode, resp.RequestID, p.Truncate(string(respBody), 1000)))

				decision := p.Health(ctx, "retry", resp.StatusCode, resp.Headers, respBody, input.RequestModel)
				if decision.Generic {
					return early(p.HandleError(ctx, input.RequestModel, false))
				}
				p.Observe(Notice{
					Platform:           in.Platform,
					AccountID:          in.AccountID,
					AccountName:        in.AccountName,
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
				return true, p.FailoverError(resp.StatusCode, respBody, decision.RetrySameAccount)
			}
			return early(p.HandleError(ctx, input.RequestModel, true))
		}

		if resp.StatusCode >= 400 && ShouldFailover(resp.StatusCode) {
			respBody, _ := p.ReadErrorBody()
			p.ResetErrorBody(respBody)

			p.Log(fmt.Sprintf("[Anthropic Passthrough] Upstream error (failover): Account=%d(%s) Status=%d RequestID=%s Body=%s",
				in.AccountID, in.AccountName, resp.StatusCode, resp.RequestID, p.Truncate(string(respBody), 1000)))

			decision := p.Health(ctx, "failover", resp.StatusCode, resp.Headers, respBody, input.RequestModel)
			if decision.Generic {
				return early(p.HandleError(ctx, input.RequestModel, false))
			}
			p.Observe(Notice{
				Platform:           in.Platform,
				AccountID:          in.AccountID,
				AccountName:        in.AccountName,
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
			return true, p.FailoverError(resp.StatusCode, respBody, decision.RetrySameAccount)
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
			if partial := PartialUsage(resp, streamResult, input.OriginalModel, input.RequestModel, input.StartTime, requestSpeed, p.IsFailover(err)); partial != nil {
				return partial, err
			}
		}
		return nil, err
	}
	usage := &attempt.Usage
	firstTokenMs := attempt.FirstTokenMs
	clientDisconnect := attempt.ClientDisconnect
	return &Result{
		RequestID:                   resp.RequestID,
		UpstreamHeaders:             resp.Headers,
		Usage:                       *usage,
		Model:                       input.OriginalModel,
		UpstreamModel:               input.RequestModel,
		UpstreamResponseServiceTier: p.ServiceTier(),
		Stream:                      input.RequestStream,
		Duration:                    time.Since(input.StartTime),
		FirstTokenMs:                firstTokenMs,
		ClientDisconnect:            clientDisconnect,
	}, nil
}

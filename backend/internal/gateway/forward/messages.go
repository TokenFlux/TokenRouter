package forward

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
)

// Messages 保留单账号请求准备、原生执行及错误/部分用量的原时序。
func Messages(ctx context.Context, p MessagePorts, in MessageInput, parsed *requeststate.ParsedRequest) (*Result, error) {
	startTime := time.Now()
	if parsed == nil {
		return nil, fmt.Errorf("parse request: empty request")
	}

	if in.AccountPresent && p.ShouldEmulate(ctx, parsed.GroupID, parsed.Body.Bytes()) {
		return p.Emulate(ctx, parsed)
	}

	if in.AccountPresent && in.Passthrough {
		passthroughBody := parsed.Body.Bytes()
		passthroughModel := parsed.Model
		if passthroughModel != "" {
			if mappedModel := p.MappedModel(passthroughModel); mappedModel != passthroughModel {
				passthroughBody = p.ReplaceModel(passthroughBody, mappedModel)
				p.Log(fmt.Sprintf("Passthrough model mapping: %s -> %s (account: %s)", parsed.Model, mappedModel, in.AccountName))
				passthroughModel = mappedModel
			}
		}
		return p.Passthrough(ctx, PassthroughInput{Body: passthroughBody, Parsed: parsed, RequestModel: passthroughModel, OriginalModel: parsed.Model, Stream: parsed.Stream, StartedAt: startTime})
	}

	if in.AccountPresent && in.Bedrock {
		return p.Bedrock(ctx, parsed, startTime)
	}

	done, err := p.Begin()
	if err != nil {
		return nil, err
	}
	defer done()
	if in.Platform == "anthropic" && in.HTTPPresent {
		if err := p.Beta(ctx, parsed.Model); err != nil {
			return nil, err
		}
	}

	body := parsed.Body.Bytes()
	replaceBody := func(next []byte) error {
		if err := parsed.ReplaceBody(next); err != nil {
			return fmt.Errorf("rewrite request body: %w", err)
		}
		body = parsed.Body.Bytes()
		return nil
	}
	reqModel := parsed.Model
	reqStream := parsed.Stream
	originalModel := reqModel

	if in.HTTPPresent {
		p.DebugOriginal(body, reqModel, reqStream)
	}

	// 账号模型映射必须先于平台模型规范化执行，确保调度、限制检查和实际转发使用同一条链路。
	accountMappedModel := p.AccountMappedModel(reqModel)
	if accountMappedModel != reqModel {
		if err := replaceBody(p.ReplaceModel(body, accountMappedModel)); err != nil {
			return nil, err
		}
		reqModel = accountMappedModel
		parsed.Model = accountMappedModel
		p.Log(fmt.Sprintf("Model mapping applied: %s -> %s (account: %s, source=account)", originalModel, accountMappedModel, in.AccountName))
	}

	// Claude Code 客户端判定：UA 匹配 claude-cli/* 且携带 metadata.user_id。
	// 真正的 Claude Code 客户端自带完整的 system prompt、cache_control 断点和 header，
	// 不需要代理做任何 body 级别的 mimicry；强行替换反而会破坏客户端的缓存策略
	// （长 system prompt 被替换为 ~45 tokens 的短 prompt，低于 Anthropic 1024 token
	// 最低缓存门槛，导致系统级缓存失效）。
	// 对于非 Claude Code 的第三方客户端（opencode 等），仍然走完整 mimicry。
	isClaudeCode := p.IsClaudeCode(ctx, body, parsed.MetadataUserID)

	shouldMimicClaudeCode := in.OAuth && !isClaudeCode

	if shouldMimicClaudeCode {

		systemRewritten := false
		systemPromptInjectionEnabled, systemPrompt, systemPromptBlocks := p.SystemSettings(ctx)
		if systemPromptInjectionEnabled {
			if err := replaceBody(p.RewriteSystem(body, parsed, systemPrompt, systemPromptBlocks)); err != nil {
				return nil, err
			}
			systemRewritten = true
		}

		// system 被重写时保留 CC prompt 的 cache_control: ephemeral（匹配真实 Claude Code 行为）；
		// 未重写时（注入开关关闭）剥离客户端 cache_control，与原有行为一致。
		// 两种情况下 enforceCacheControlLimit 都会兜底处理上限。
		normalizeOpts := NormalizeOptions{StripSystemCacheControl: !systemRewritten}
		if metadata := p.Metadata(ctx, parsed); metadata != "" {
			normalizeOpts.InjectMetadata = true
			normalizeOpts.MetadataUserID = metadata
		}

		var normalizedBody []byte
		normalizedBody, reqModel = p.NormalizeOAuth(body, reqModel, normalizeOpts)
		if err := replaceBody(normalizedBody); err != nil {
			return nil, err
		}

		if err := replaceBody(p.RewriteCache(ctx, body)); err != nil {
			return nil, err
		}
		if next, found := p.RewriteTools(body); found {
			if err := replaceBody(next); err != nil {
				return nil, err
			}
			p.BindTools()
		} else if err := replaceBody(p.ToolsLast(body)); err != nil {
			return nil, err
		}

	}

	if next, ok := p.NormalizeDateline(ctx, body); ok {
		if err := replaceBody(next); err != nil {
			return nil, err
		}
	}

	if err := replaceBody(p.CacheLimit(body)); err != nil {
		return nil, err
	}

	mappedModel := p.PlatformModel(reqModel)
	if mappedModel != reqModel {
		if err := replaceBody(p.ReplaceModel(body, mappedModel)); err != nil {
			return nil, err
		}
		reqModel = mappedModel
		parsed.Model = mappedModel
		p.Log(fmt.Sprintf("Platform model normalization applied: %s -> %s (account: %s)", accountMappedModel, mappedModel, in.AccountName))
	}

	if p.InjectTTL(ctx) {
		if err := replaceBody(p.CacheTTL(body)); err != nil {
			return nil, err
		}
	}

	if err := p.Credential(ctx); err != nil {
		return nil, err
	}
	p.Transport()

	if err := replaceBody(protocolanthropic.StripEmptyTextBlocks(body)); err != nil {
		return nil, err
	}

	if err := replaceBody(p.FilterSearchHistory(body, reqModel)); err != nil {
		return nil, err
	}

	if err := replaceBody(p.FilterThinking(body, reqModel)); err != nil {
		return nil, err
	}
	if p.PassbackThinking(reqModel) {
		if rewritten, applied := p.NormalizeThinking(body, reqModel); applied {
			if err := replaceBody(rewritten); err != nil {
				return nil, err
			}
			p.Log(fmt.Sprintf("Account %d: rewrote thinking.type for %s", in.AccountID, reqModel))
		}
	}

	var resp *ExchangeResponse
	lastWireBody := body
	var earlyResult *Result
	stopped := false
	var streamResult *StreamOutcome
	writerSizeBeforeStream := 0
	before := func(ctx context.Context, response *ExchangeResponse, wire []byte) (bool, error) {
		resp = response
		lastWireBody = wire
		early := func(result *Result, err error) (bool, error) { earlyResult = result; return true, err }
		if resp.StatusCode >= 400 && ShouldRetry(in.OAuth, resp.StatusCode) {
			if ShouldFailover(resp.StatusCode) {
				respBody, _ := p.ReadErrorBody()
				p.ResetErrorBody(respBody)

				p.Log(fmt.Sprintf("[Forward] Upstream error (retry exhausted, failover): Account=%d(%s) Status=%d RequestID=%s Body=%s",
					in.AccountID, in.AccountName, resp.StatusCode, resp.RequestID, p.Truncate(string(respBody), 1000)))

				decision := p.Health(ctx, "retry", resp.StatusCode, resp.Headers, respBody, reqModel)
				if decision.Generic {
					return early(p.HandleError(ctx, reqModel, false))
				}
				p.Observe(Notice{
					Platform:           in.Platform,
					AccountID:          in.AccountID,
					AccountName:        in.AccountName,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.RequestID,
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
			return early(p.HandleError(ctx, reqModel, true))
		}

		if resp.StatusCode >= 400 && ShouldFailover(resp.StatusCode) {
			respBody, _ := p.ReadErrorBody()
			p.ResetErrorBody(respBody)

			p.Log(fmt.Sprintf("[Forward] Upstream error (failover): Account=%d(%s) Status=%d RequestID=%s Body=%s",
				in.AccountID, in.AccountName, resp.StatusCode, resp.RequestID, p.Truncate(string(respBody), 1000)))

			decision := p.Health(ctx, "failover", resp.StatusCode, resp.Headers, respBody, reqModel)
			if decision.Generic {
				return early(p.HandleError(ctx, reqModel, false))
			}
			p.Observe(Notice{
				Platform:           in.Platform,
				AccountID:          in.AccountID,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.RequestID,
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

			if resp.StatusCode == 400 && in.FailoverOn400 {
				respBody, readErr := p.ReadErrorBody()
				if readErr != nil {

					return early(p.HandleError(ctx, reqModel, false))
				}
				p.ResetErrorBody(respBody)

				if p.Failover400(respBody) {
					upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(respBody))
					upstreamMsg = p.Sanitize(upstreamMsg)
					upstreamDetail := ""
					if in.LogErrorBody {
						maxBytes := in.LogErrorBodyMaxBytes
						if maxBytes <= 0 {
							maxBytes = 2048
						}
						upstreamDetail = p.Truncate(string(respBody), maxBytes)
					}
					p.Observe(Notice{
						Platform:           in.Platform,
						AccountID:          in.AccountID,
						AccountName:        in.AccountName,
						UpstreamStatusCode: resp.StatusCode,
						UpstreamRequestID:  resp.RequestID,
						Kind:               "failover_on_400",
						Message:            upstreamMsg,
						Detail:             upstreamDetail,
					})

					if in.LogErrorBody {
						p.Log(fmt.Sprintf(
							"Account %d: 400 error, attempting failover: %s",
							in.AccountID,
							p.TruncateBytes(respBody, in.LogErrorBodyMaxBytes),
						))
					} else {
						p.Log(fmt.Sprintf("Account %d: 400 error, attempting failover", in.AccountID))
					}
					decision := p.Health(ctx, "failover", resp.StatusCode, resp.Headers, respBody, reqModel)
					if decision.Generic {
						return early(p.HandleError(ctx, reqModel, false))
					}
					return true, p.FailoverError(resp.StatusCode, respBody, decision.RetrySameAccount)
				}
			}
			return early(p.HandleError(ctx, reqModel, false))
		}

		if !bytes.Equal(lastWireBody, body) {

			if err := replaceBody(lastWireBody); err != nil {
				return true, err
			}
		}

		return false, nil
	}
	attempt, err := p.Execute(ctx, MessageExecution{Model: reqModel, OriginalModel: originalModel, Stream: reqStream, Mimic: shouldMimicClaudeCode, StartedAt: startTime, Body: body, ReplaceBody: replaceBody, Hooks: MessageHooks{
		Before: func(ctx context.Context, response *ExchangeResponse, wire []byte) (bool, error) {
			var err error
			stopped, err = before(ctx, response, wire)
			return stopped, err
		},
		Wire: func(wire []byte) { lastWireBody = wire },
		Accepted: func() {
			if parsed.OnUpstreamAccepted != nil {
				parsed.OnUpstreamAccepted()
			}
		},
		BeforeStream: func() { writerSizeBeforeStream = p.Size() },
		Stream: func(result *StreamOutcome) {
			if result != nil {
				streamResult = result
			}
		},
	}})

	if stopped {
		return earlyResult, err
	}
	if reqStream {
		if err != nil {
			if raw, ok := p.StreamError(err); ok {

				body := []byte(raw)
				semanticStatus := 403
				var semanticDecision ErrorDecision
				if p.Size() == writerSizeBeforeStream && gjson.GetBytes(body, "error.type").String() == "overloaded_error" {
					semanticStatus = 529
					semanticDecision = p.Health(ctx, "failover_synthetic", semanticStatus, resp.Headers, body, reqModel)
				}
				upstreamMsg := p.Sanitize(strings.TrimSpace(upstream.ExtractErrorMessage(body)))

				upstreamDetail := ""
				if in.LogErrorBody {
					maxBytes := in.LogErrorBodyMaxBytes
					if maxBytes <= 0 {
						maxBytes = 2048
					}
					upstreamDetail = p.Truncate(raw, maxBytes)
				}

				p.Observe(Notice{
					Platform:           in.Platform,
					AccountID:          in.AccountID,
					AccountName:        in.AccountName,
					UpstreamStatusCode: semanticStatus,
					UpstreamRequestID:  resp.RequestID,
					Kind:               "stream_error",
					Message:            upstreamMsg,
					Detail:             upstreamDetail,
				})

				p.Log(fmt.Sprintf(
					"[Forward] SSE error event in stream: Account=%d(%s) RequestID=%s Body=%s",
					in.AccountID, in.AccountName, resp.RequestID,
					p.Truncate(raw, 1000),
				))

				decision := semanticDecision
				if semanticStatus != 529 {
					decision = p.Health(ctx, "volatile", 403, resp.Headers, body, reqModel)
					if p.Size() == writerSizeBeforeStream {
						decision = p.Health(ctx, "persist", 403, resp.Headers, body, reqModel)
					}
				}
				if decision.Generic && !p.Written() {
					p.GenericError()
					return nil, fmt.Errorf("upstream SSE error not in custom error codes")
				}
				if !p.Written() && decision.Failover {
					return nil, p.FailoverError(semanticStatus, body, decision.RetrySameAccount)
				}
				return nil, err
			}
			requestSpeed := gjson.GetBytes(lastWireBody, "speed").String()
			if partial := PartialUsage(resp, streamResult, originalModel, mappedModel, startTime, requestSpeed, p.IsFailover(err)); partial != nil {
				return partial, err
			}
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	usage := &attempt.Usage
	firstTokenMs := attempt.FirstTokenMs
	clientDisconnect := attempt.ClientDisconnect
	if resp == nil {
		return nil, err
	}
	return &Result{
		RequestID:                   resp.RequestID,
		UpstreamHeaders:             resp.Headers,
		Usage:                       *usage,
		Model:                       originalModel,
		UpstreamModel:               mappedModel,
		UpstreamResponseServiceTier: p.ServiceTier(),
		Stream:                      reqStream,
		Duration:                    time.Since(startTime),
		FirstTokenMs:                firstTokenMs,
		ClientDisconnect:            clientDisconnect,
	}, nil
}

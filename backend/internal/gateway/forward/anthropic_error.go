package forward

import (
	"context"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// AnthropicError 保留账号策略、提交标记、规则匹配和安全消息的原有顺序。
func AnthropicError(ctx context.Context, p ErrorPorts, in ErrorInput) (*Result, error) {
	// 上游返回非成功 HTTP 状态，仍应计入 Ollama Cloud 活动。
	p.ScheduleActivity()
	body, readErr := p.ReadBody()
	if readErr != nil {
		// 读取失败时 body 可能被截断，错误分类会基于不完整数据；记录日志以便排查，
		// 避免静默吞掉导致误判。
		p.Log(fmt.Sprintf("[Forward] Failed to fully read upstream error body: Account=%d(%s) Status=%d err=%v",
			in.AccountID, in.AccountName, in.Status, readErr))
	}

	// 调试日志：打印上游错误响应
	p.Log(fmt.Sprintf("[Forward] Upstream error (non-retryable): Account=%d(%s) Status=%d RequestID=%s Body=%s",
		in.AccountID, in.AccountName, in.Status, in.RequestID, p.Truncate(string(body), 1000)))

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	upstreamMsg = p.Sanitize(upstreamMsg)

	p.ScopeDiagnostic(upstreamMsg, in.Status, in.RequestID)

	upstreamDetail := ""
	if in.LogBody {
		maxBytes := in.LogBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = p.Truncate(string(body), maxBytes)
	}
	p.SetError(in.Status, upstreamMsg, upstreamDetail)
	p.Observe(Notice{
		Platform:           in.Platform,
		AccountID:          in.AccountID,
		UpstreamStatusCode: in.Status,
		UpstreamRequestID:  in.RequestID,
		Kind:               "http_error",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})

	// 处理上游错误，并区分显式策略、自定义未命中和池模式默认绕过。
	decision := p.Health(ctx, in.Status, in.RequestedModels)
	if decision.Generic {
		p.Commit()
		p.Message(500, "upstream_error", "Upstream gateway error")
		return nil, fmt.Errorf("upstream error: %d (not in custom error codes)", in.Status)
	}
	if decision.Failover {
		return nil, p.Failover(in.Status, body, decision.RetrySameAccount)
	}

	p.Commit()

	// 记录上游错误响应体摘要便于排障（可选：由配置控制；不回显到客户端）
	if in.LogBody {
		p.Log(fmt.Sprintf(
			"Upstream error %d (account=%d platform=%s type=%s): %s",
			in.Status,
			in.AccountID,
			in.Platform,
			in.AccountType,
			p.TruncateBytes(body, in.LogBodyMaxBytes),
		))
	}

	// 非 failover 错误也支持错误透传规则匹配。
	if status, errType, errMsg, matched := applyAnthropicDisplayRule(
		p,
		in.Platform,
		in.Status,
		body,
		502,
		"upstream_error",
		"Upstream request failed",
	); matched {
		p.Message(status, errType, errMsg)

		summary := upstreamMsg
		if summary == "" {
			summary = errMsg
		}
		if summary == "" {
			return nil, fmt.Errorf("upstream error: %d (passthrough rule matched)", in.Status)
		}
		return nil, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", in.Status, summary)
	}

	// 根据状态码返回适当的自定义错误响应（不透传上游详细信息）
	var errType, errMsg string
	var statusCode int

	switch in.Status {
	case 400:
		p.Raw(400, body)
		summary := upstreamMsg
		if summary == "" {
			summary = p.TruncateBytes(body, 512)
		}
		if summary == "" {
			return nil, fmt.Errorf("upstream error: %d", in.Status)
		}
		return nil, fmt.Errorf("upstream error: %d message=%s", in.Status, summary)
	case 401:
		statusCode = 502
		errType = "upstream_error"
		errMsg = "Upstream authentication failed, please contact administrator"
	case 403:
		statusCode = 502
		errType = "upstream_error"
		errMsg = "Upstream access forbidden, please contact administrator"
	case 429:
		statusCode = 429
		errType = "rate_limit_error"
		errMsg = "Upstream rate limit exceeded, please retry later"
	case 529:
		statusCode = 503
		errType = "overloaded_error"
		errMsg = "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		statusCode = 502
		errType = "upstream_error"
		errMsg = "Upstream service temporarily unavailable"
	default:
		statusCode = 502
		errType = "upstream_error"
		errMsg = "Upstream request failed"
	}

	// 返回自定义错误响应
	p.Message(statusCode, errType, errMsg)

	if upstreamMsg == "" {
		return nil, fmt.Errorf("upstream error: %d", in.Status)
	}
	return nil, fmt.Errorf("upstream error: %d message=%s", in.Status, upstreamMsg)
}

// AnthropicRetryError 保留账号策略、提交标记、规则匹配和安全消息的原有顺序。
func AnthropicRetryError(ctx context.Context, p ErrorPorts, in ErrorInput) (*Result, error) {
	respBody, _ := p.ReadBody()
	p.ResetBody(respBody)

	decision := p.RetryHealth(ctx, in.RequestedModels)
	if decision.Generic {
		return AnthropicError(ctx, p, in)
	}
	if decision.Failover {
		return nil, p.Failover(in.Status, respBody, decision.RetrySameAccount)
	}
	p.Commit()

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(respBody))
	upstreamMsg = p.Sanitize(upstreamMsg)

	p.ScopeDiagnostic(upstreamMsg, in.Status, in.RequestID)

	upstreamDetail := ""
	if in.LogBody {
		maxBytes := in.LogBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = p.Truncate(string(respBody), maxBytes)
	}
	p.SetError(in.Status, upstreamMsg, upstreamDetail)
	p.Observe(Notice{
		Platform:           in.Platform,
		AccountID:          in.AccountID,
		UpstreamStatusCode: in.Status,
		UpstreamRequestID:  in.RequestID,
		Kind:               "retry_exhausted",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})

	if in.LogBody {
		p.Log(fmt.Sprintf(
			"Upstream error %d retries_exhausted (account=%d platform=%s type=%s): %s",
			in.Status,
			in.AccountID,
			in.Platform,
			in.AccountType,
			p.TruncateBytes(respBody, in.LogBodyMaxBytes),
		))
	}

	if status, errType, errMsg, matched := applyAnthropicDisplayRule(
		p,
		in.Platform,
		in.Status,
		respBody,
		502,
		"upstream_error",
		"Upstream request failed after retries",
	); matched {
		p.Message(status, errType, errMsg)

		summary := upstreamMsg
		if summary == "" {
			summary = errMsg
		}
		if summary == "" {
			return nil, fmt.Errorf("upstream error: %d (retries exhausted, passthrough rule matched)", in.Status)
		}
		return nil, fmt.Errorf("upstream error: %d (retries exhausted, passthrough rule matched) message=%s", in.Status, summary)
	}

	// 返回统一的重试耗尽错误响应
	p.Message(502, "upstream_error", "Upstream request failed after retries")

	if upstreamMsg == "" {
		return nil, fmt.Errorf("upstream error: %d (retries exhausted)", in.Status)
	}
	return nil, fmt.Errorf("upstream error: %d (retries exhausted) message=%s", in.Status, upstreamMsg)
}

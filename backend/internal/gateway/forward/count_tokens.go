package forward

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// CountTokens 保留无计費端点的准备、单次签名修复和返回语义。
func CountTokens(ctx context.Context, p CountPorts, in MessageInput, parsed *requeststate.ParsedRequest) error {
	if parsed == nil {
		p.CountError(400, "invalid_request_error", "Request body is empty")
		return fmt.Errorf("parse request: empty request")
	}

	if in.AccountPresent && in.Passthrough {
		passthroughBody := parsed.Body.Bytes()
		mappedModel := parsed.Model
		if reqModel := parsed.Model; reqModel != "" {
			if resolvedModel := p.ResolveModel(ctx, reqModel); resolvedModel != "" {
				mappedModel = resolvedModel
			}
			if mappedModel != reqModel {
				passthroughBody = p.ReplaceModel(passthroughBody, mappedModel)
				p.Log(fmt.Sprintf("CountTokens passthrough model mapping: %s -> %s (account: %s)", reqModel, mappedModel, in.AccountName))
			}
		}
		return CountPassthrough(ctx, p, in, passthroughBody, mappedModel)
	}

	// Bedrock 不支持 count_tokens 端点
	if in.AccountPresent && in.Bedrock {
		p.CountError(404, "not_found_error", "count_tokens endpoint is not supported for Bedrock")
		return nil
	}

	// Antigravity/Qoder 账户不支持 count_tokens，返回 404 让客户端 fallback 到本地估算。
	// 返回 nil 避免 handler 层记录为错误，也不设置 ops 上游错误上下文。
	if in.Platform == "antigravity" || in.Platform == "qoder" {
		p.CountError(404, "not_found_error", "count_tokens endpoint is not supported for this platform")
		return nil
	}

	body := parsed.Body.Bytes()
	replaceBody := func(next []byte) error {
		if err := parsed.ReplaceBody(next); err != nil {
			return fmt.Errorf("rewrite count_tokens body: %w", err)
		}
		body = parsed.Body.Bytes()
		return nil
	}
	reqModel := parsed.Model

	// count_tokens 与 messages 主路径共用账号映射和平台规范化顺序，确保真正发送的模型与调度结果一致。
	if reqModel != "" {
		upstreamModel := p.ResolveModel(ctx, reqModel)
		if upstreamModel != "" && upstreamModel != reqModel {
			originalReqModel := reqModel
			if err := replaceBody(p.ReplaceModel(body, upstreamModel)); err != nil {
				return err
			}
			reqModel = upstreamModel
			parsed.Model = upstreamModel
			p.Log(fmt.Sprintf("CountTokens final model applied: %s -> %s (account: %s)", originalReqModel, upstreamModel, in.AccountName))
		}
	}

	if err := replaceBody(protocolanthropic.StripEmptyTextBlocks(body)); err != nil {
		return err
	}

	isClaudeCodeCT := p.IsCountClaudeCode(ctx, parsed.MetadataUserID)
	shouldMimicClaudeCode := in.OAuth && !isClaudeCodeCT

	if shouldMimicClaudeCode {
		normalizeOpts := NormalizeOptions{StripSystemCacheControl: true}
		var normalizedBody []byte
		normalizedBody, reqModel = p.NormalizeOAuth(body, reqModel, normalizeOpts)
		if err := replaceBody(normalizedBody); err != nil {
			return err
		}

		if err := replaceBody(p.RewriteCache(ctx, body)); err != nil {
			return err
		}
		if next, found := p.RewriteTools(body); found {
			if err := replaceBody(next); err != nil {
				return err
			}
		} else if err := replaceBody(p.ToolsLast(body)); err != nil {
			return err
		}
	}

	// 获取凭证
	err := p.Credential(ctx)
	if err != nil {
		p.CountError(502, "upstream_error", "Failed to get access token")
		return err
	}

	// 构建上游请求
	wireBody, err := p.BuildCount(ctx, body, reqModel, shouldMimicClaudeCode, false)
	if err != nil {
		p.CountError(500, "api_error", "Failed to build request")
		return err
	}
	// 先记录首发 wire body；如果后面进入 400 retry，retry 会基于未签名的逻辑 body 重新构建。
	acceptedWireBody := wireBody

	// 获取代理URL（自定义 base URL 模式下，proxy 通过 buildCustomRelayURL 作为查询参数传递）
	resp, err := p.SendCount(ctx, false)
	if err != nil {
		p.SetError(0, p.Sanitize(err.Error()), "")
		p.CountError(502, "upstream_error", "Request failed")
		return fmt.Errorf("upstream request failed: %w", err)
	}

	// 读取响应体

	respBody, err := p.ReadCount()
	if err != nil {
		if !p.IsTooLarge(err) {
			p.CountError(502, "upstream_error", "Failed to read response")
		}
		return err
	}

	// 检测 thinking block 签名错误（400）并重试一次（过滤 thinking blocks）
	if resp.StatusCode == 400 && p.RectifyCount(ctx, respBody, reqModel) {
		p.Log(fmt.Sprintf("Account %d: detected thinking block signature error on count_tokens, retrying with filtered thinking blocks", in.AccountID))

		filteredBody := p.FilterCountRetry(body, reqModel)
		retryWireBody, buildErr := p.BuildCount(ctx, filteredBody, reqModel, shouldMimicClaudeCode, false)
		if buildErr == nil {
			retryResp, retryErr := p.SendCount(ctx, false)
			if retryErr == nil {
				if retryResp.StatusCode < 400 {
					// count_tokens thinking 签名重试成功后记录最终 wire body，错误响应仍保留原 body 便于后续处理。
					acceptedWireBody = retryWireBody
				}
				resp = retryResp
				respBody, err = p.ReadCount()
				if err != nil {
					if !p.IsTooLarge(err) {
						p.CountError(502, "upstream_error", "Failed to read response")
					}
					return err
				}
			}
		}
	}

	if resp.StatusCode < 400 && !bytes.Equal(acceptedWireBody, body) {
		// count_tokens 成功后再同步最终 wire body，避免后续逻辑继续从重试前 body 派生。
		if err := replaceBody(acceptedWireBody); err != nil {
			return err
		}
	}

	// 处理错误响应
	if resp.StatusCode >= 400 {
		upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(respBody))
		upstreamMsg = p.Sanitize(upstreamMsg)
		if p.UnsupportedCount(resp.StatusCode, respBody) {
			p.CountError(404, "not_found_error", "count_tokens endpoint is not supported by upstream")
			return nil
		}
		decision := p.CountHealth(ctx, resp.StatusCode, resp.Headers, respBody, reqModel)
		if decision.Generic {
			p.CountError(500, "upstream_error", "Upstream gateway error")
			return fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		if decision.Failover {
			return p.CountFailover(resp.StatusCode, resp.Headers, respBody, decision.RetrySameAccount)
		}
		upstreamDetail := ""
		if in.LogErrorBody {
			maxBytes := in.LogErrorBodyMaxBytes
			if maxBytes <= 0 {
				maxBytes = 2048
			}
			upstreamDetail = p.Truncate(string(respBody), maxBytes)
		}
		p.SetError(resp.StatusCode, upstreamMsg, upstreamDetail)

		// 记录上游错误摘要便于排障（不回显请求内容）
		if in.LogErrorBody {
			p.Log(fmt.Sprintf(
				"count_tokens upstream error %d (account=%d platform=%s type=%s): %s",
				resp.StatusCode,
				in.AccountID,
				in.Platform,
				in.AccountType,
				p.TruncateBytes(respBody, in.LogErrorBodyMaxBytes),
			))
		}

		// 返回简化的错误响应
		errMsg := "Upstream request failed"
		switch resp.StatusCode {
		case 429:
			errMsg = "Rate limit exceeded"
		case 529:
			errMsg = "Service overloaded"
		}
		p.CountError(resp.StatusCode, "upstream_error", errMsg)
		if upstreamMsg == "" {
			return fmt.Errorf("upstream error: %d", resp.StatusCode)
		}
		return fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	// 透传成功响应
	p.CountSuccess(resp.StatusCode, resp.Headers, respBody, false)
	return nil
}

// CountPassthrough 保留无计費端点的准备、单次签名修复和返回语义。
func CountPassthrough(ctx context.Context, p CountPorts, in MessageInput, body []byte, mappedModel string) error {
	err := p.Credential(ctx)
	if err != nil {
		p.CountError(502, "upstream_error", "Failed to get access token")
		return err
	}
	if tokenType := p.TokenKind(); tokenType != "apikey" {
		p.CountError(502, "upstream_error", "Invalid account token type")
		return fmt.Errorf("anthropic api key passthrough requires apikey token, got: %s", tokenType)
	}

	_, err = p.BuildCount(ctx, body, mappedModel, false, true)
	if err != nil {
		p.CountError(500, "api_error", "Failed to build request")
		return err
	}

	resp, err := p.SendCount(ctx, true)
	if err != nil {
		p.SetError(0, p.Sanitize(err.Error()), "")
		p.Observe(Notice{
			Platform:           in.Platform,
			AccountID:          in.AccountID,
			AccountName:        in.AccountName,
			UpstreamStatusCode: 0,
			UpstreamURL:        p.CountURL(),
			Passthrough:        true,
			Kind:               "request_error",
			Message:            p.Sanitize(err.Error()),
		})
		p.CountError(502, "upstream_error", "Request failed")
		return fmt.Errorf("upstream request failed: %w", err)
	}

	respBody, err := p.ReadCount()
	if err != nil {
		if !p.IsTooLarge(err) {
			p.CountError(502, "upstream_error", "Failed to read response")
		}
		return err
	}

	if resp.StatusCode >= 400 {
		upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(respBody))
		upstreamMsg = p.Sanitize(upstreamMsg)

		// 中转站不支持 count_tokens 端点时（404），返回 404 让客户端 fallback 到本地估算。
		// 仅在错误消息明确指向 count_tokens endpoint 不存在时生效，避免误吞其他 404（如错误 base_url）。
		// 返回 nil 避免 handler 层记录为错误，也不设置 ops 上游错误上下文。
		if p.UnsupportedCount(resp.StatusCode, respBody) {
			p.Log(fmt.Sprintf(
				"[count_tokens] Upstream does not support count_tokens (404), returning 404: account=%d name=%s msg=%s",
				in.AccountID, in.AccountName, p.Truncate(upstreamMsg, 512)))
			p.CountError(404, "not_found_error", "count_tokens endpoint is not supported by upstream")
			return nil
		}
		decision := p.CountHealth(ctx, resp.StatusCode, resp.Headers, respBody, mappedModel)
		if decision.Generic {
			p.CountError(500, "upstream_error", "Upstream gateway error")
			return fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		if decision.Failover {
			return p.CountFailover(resp.StatusCode, resp.Headers, respBody, decision.RetrySameAccount)
		}

		upstreamDetail := ""
		if in.LogErrorBody {
			maxBytes := in.LogErrorBodyMaxBytes
			if maxBytes <= 0 {
				maxBytes = 2048
			}
			upstreamDetail = p.Truncate(string(respBody), maxBytes)
		}
		p.SetError(resp.StatusCode, upstreamMsg, upstreamDetail)
		p.Observe(Notice{
			Platform:           in.Platform,
			AccountID:          in.AccountID,
			AccountName:        in.AccountName,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.RequestID,
			UpstreamURL:        p.CountURL(),
			Passthrough:        true,
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})

		errMsg := "Upstream request failed"
		switch resp.StatusCode {
		case 429:
			errMsg = "Rate limit exceeded"
		case 529:
			errMsg = "Service overloaded"
		}
		p.CountError(resp.StatusCode, "upstream_error", errMsg)
		if upstreamMsg == "" {
			return fmt.Errorf("upstream error: %d", resp.StatusCode)
		}
		return fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	p.CountSuccess(resp.StatusCode, resp.Headers, respBody, true)
	return nil
}

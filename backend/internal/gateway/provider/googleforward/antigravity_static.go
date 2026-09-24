package googleforward

import (
	"context"
	"fmt"
	"net/http"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// ForwardUpstream 衔接账号与 HTTP 错误策略和结果，单次执行由原生模块关闭资源。
func (s *Antigravity) ForwardUpstream(ctx context.Context, output Output, account *gatewayprovider.ExecutionAccount, body []byte) (*forwardcore.MessagesResult, error) {
	c := &attempt{Output: output}

	started := time.Now()
	prefix := logPrefix(c.GetHeader("session_id"), account.Record.Name)
	req, model, stream, err := antigravity.BuildStaticRequest(ctx, body, antigravity.StaticRequestInput{
		BaseURL:  account.View().GetCredential("base_url"),
		APIKey:   account.View().GetCredential("api_key"),
		Version:  c.GetHeader("anthropic-version"),
		Beta:     c.GetHeader("anthropic-beta"),
		Sanitize: anthropic.SanitizeAnthropicBodyForBetaTokens,
	})
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}
	handled := false
	target := &antigravity.Target{
		AccountID: account.Record.ID,
		Model:     model,
		Mode:      antigravity.ModeStaticClaudeResponse,
		StartedAt: started,
		Response:  s.antigravityResponseAdapter(c).Options,
		Enter:     s.Enter,

		Exchange: func(context.Context) (*http.Response, error) {
			resp, err := s.Transport.Do(req, proxyURL, account.Record.ID, account.Record.Concurrency)
			if err != nil {
				logging.LegacyPrintf("service.antigravity_gateway", "%s upstream request failed: %v", prefix, err)
				return nil, fmt.Errorf("upstream request failed: %w", err)
			}
			return resp, nil
		},

		BeforeResponse: func(ctx context.Context, resp *http.Response) (bool, error) {
			if resp.StatusCode < 400 {
				return false, nil
			}
			respBody := s.readUpstreamErrorBody(resp)
			if resp.StatusCode == http.StatusTooManyRequests {
				s.handleUpstreamError(ctx, prefix, account, resp.StatusCode, resp.Header, respBody, model, 0, "", false)
			}
			c.Raw(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
			handled = true
			return true, nil
		},
	}
	result, err := (antigravity.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocol.ProtocolAnthropicMessages, Body: body, ResponseModel: model, Stream: stream, Target: target}, c.Sink())
	if err != nil {
		return nil, err
	}
	if handled {
		return &forwardcore.MessagesResult{Model: model}, nil
	}
	logging.LegacyPrintf("service.antigravity_gateway", "%s status=success duration_ms=%d", prefix, result.Duration.Milliseconds())
	return &forwardcore.MessagesResult{
		Model:            model,
		UpstreamHeaders:  result.UpstreamHeaders,
		Stream:           stream,
		Duration:         result.Duration,
		FirstTokenMs:     result.FirstTokenMs,
		ClientDisconnect: result.ClientDisconnect,
		Usage: upstream.TokenUsage{
			InputTokens:              result.Usage.InputTokens,
			OutputTokens:             result.Usage.OutputTokens,
			CacheReadInputTokens:     result.Usage.CacheReadInputTokens,
			CacheCreationInputTokens: result.Usage.CacheCreationInputTokens,
		},
	}, nil
}

// API Key 直通保留独立重试策略：400 不做请求体降级。
package anthropic

import (
	"bytes"
	"context"
	"net/http"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

func ExchangePassthrough(ctx context.Context, body []byte, options ExchangeOptions) (*http.Response, []byte, error) {
	var resp *http.Response
	lastWireBody := body
	retryStart := time.Now()
	for attempt := 1; attempt <= options.MaxAttempts; attempt++ {
		upstreamCtx, releaseUpstreamCtx := options.Context(ctx, options.Stream)
		upstreamReq, wireBody, err := options.Build(upstreamCtx, body)
		releaseUpstreamCtx()
		if err != nil {
			return nil, lastWireBody, err
		}
		lastWireBody = wireBody
		if options.SynchronizeBody && !bytes.Equal(wireBody, body) {
			// build 阶段会按 beta 能力清理 body，发送前同步到 ParsedRequest 当前视图。
			if err := options.ReplaceBody(wireBody); err != nil {
				return nil, lastWireBody, err
			}
			body = wireBody
		}

		resp, err = options.Do(upstreamReq)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			return nil, lastWireBody, options.TransportError(ctx, err, upstreamReq.URL.String())
		}

		// 透传分支禁止 400 请求体降级重试（该重试会改写请求体）
		if resp.StatusCode >= 400 && resp.StatusCode != 400 && options.ShouldRetry(resp.StatusCode) {
			if attempt < options.MaxAttempts {
				elapsed := time.Since(retryStart)
				if elapsed >= options.MaxElapsed {
					break
				}

				delay := options.Delay(attempt)
				remaining := options.MaxElapsed - elapsed
				if delay > remaining {
					delay = remaining
				}
				if delay <= 0 {
					break
				}

				respBody, _ := options.ReadErrorBody(resp)
				_ = resp.Body.Close()
				options.Observe(ExchangeNotice{
					Platform:           options.Platform,
					AccountID:          options.AccountID,
					AccountName:        options.AccountName,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.Header.Get("x-request-id"),
					UpstreamURL:        options.SafeURL(upstreamReq.URL.String()),
					Passthrough:        true,
					Kind:               "retry",
					Message:            options.ErrorMessage(respBody),
					Detail:             options.Detail(respBody),
				})
				logger.LegacyPrintf("service.gateway", "Anthropic passthrough account %d: upstream error %d, retry %d/%d after %v (elapsed=%v/%v)",
					options.AccountID, resp.StatusCode, attempt, options.MaxAttempts, delay, elapsed, options.MaxElapsed)
				if err := upstream.WaitContext(ctx, delay); err != nil {
					return nil, lastWireBody, err
				}
				continue
			}
			break
		}

		break
	}

	return resp, lastWireBody, nil
}

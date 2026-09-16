// 本文件保留同账号的既有重试边界；全局 failover 仍由调用方拥有。
package bedrock

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type RequestOptions struct {
	ModelID, Region, APIKey string
	Stream, APIKeyMode      bool
	Signer                  *BedrockSigner
	Do                      func(*http.Request) (*http.Response, error)
}
type RetryPolicy struct {
	MaxAttempts    int
	MaxElapsed     time.Duration
	Delay          func(int) time.Duration
	ShouldRetry    func(int) bool
	ReadErrorBody  func(*http.Response) ([]byte, error)
	TransportError func(error, string) error
	ObserveRetry   func(*http.Response, []byte, string, int, time.Duration)
}

func ExecuteUpstream(ctx context.Context, body []byte, options RequestOptions, policy RetryPolicy) (*http.Response, error) {
	var resp *http.Response
	var err error
	retryStart := time.Now()
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		var upstreamReq *http.Request
		if options.APIKeyMode {
			upstreamReq, err = BuildRequestAPIKey(ctx, body, options.ModelID, options.Region, options.Stream, options.APIKey)
		} else {
			upstreamReq, err = BuildRequest(ctx, body, options.ModelID, options.Region, options.Stream, options.Signer)
		}
		if err != nil {
			return nil, err
		}

		resp, err = options.Do(upstreamReq)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if policy.TransportError != nil {
				return nil, policy.TransportError(err, upstreamReq.URL.String())
			}
			return nil, err
		}

		if resp.StatusCode >= 400 && resp.StatusCode != 400 && policy.ShouldRetry != nil && policy.ShouldRetry(resp.StatusCode) {
			if attempt < policy.MaxAttempts {
				elapsed := time.Since(retryStart)
				if elapsed >= policy.MaxElapsed {
					break
				}

				delay := policy.Delay(attempt)
				remaining := policy.MaxElapsed - elapsed
				if delay > remaining {
					delay = remaining
				}
				if delay <= 0 {
					break
				}

				respBody, _ := policy.ReadErrorBody(resp)
				_ = resp.Body.Close()
				if policy.ObserveRetry != nil {
					policy.ObserveRetry(resp, respBody, upstreamReq.URL.String(), attempt, delay)
				}

				if err := upstream.WaitContext(ctx, delay); err != nil {
					return nil, err
				}
				continue
			}
			break
		}

		break
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("upstream request failed: empty response")
	}
	return resp, nil
}

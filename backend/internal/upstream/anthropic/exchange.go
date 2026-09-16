// 本文件保留 Anthropic 单账号的签名/预算恢复与有界重试，不执行账号切换或资金操作。
package anthropic

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type ExchangeNotice struct {
	Passthrough                                           bool
	Platform, AccountName                                 string
	AccountID                                             int64
	UpstreamStatusCode                                    int
	UpstreamRequestID, UpstreamURL, Kind, Message, Detail string
}
type ExchangeOptions struct {
	SynchronizeBody                            bool
	AccountID                                  int64
	Platform, AccountName                      string
	Stream, DebugHeaders                       bool
	MaxAttempts, BudgetTokens, BudgetMaxTokens int
	MaxElapsed                                 time.Duration
	Context                                    func(context.Context, bool) (context.Context, context.CancelFunc)
	Build                                      func(context.Context, []byte) (*http.Request, []byte, error)
	Do                                         func(*http.Request) (*http.Response, error)
	ReadErrorBody                              func(*http.Response) ([]byte, error)
	ShouldRectify, IsSignatureError            func(context.Context, []byte) bool
	BudgetEnabled                              func(context.Context) bool
	ShouldRetry                                func(int) bool
	FilterThinking, FilterTools                func([]byte) []byte
	IsBudgetError                              func(string) bool
	RectifyBudget                              func([]byte) ([]byte, bool)
	ReplaceBody                                func([]byte) error
	Delay                                      func(int) time.Duration
	Observe                                    func(ExchangeNotice)
	SafeURL, Sanitize                          func(string) string
	ErrorMessage, Detail                       func([]byte) string
	TransportError                             func(context.Context, error, string) error
}

func Exchange(ctx context.Context, body []byte, options ExchangeOptions) (*http.Response, []byte, error) {
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

		resp, err = options.Do(upstreamReq)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			return nil, lastWireBody, options.TransportError(ctx, err, upstreamReq.URL.String())
		}

		if resp.StatusCode == 400 {
			respBody, readErr := options.ReadErrorBody(resp)
			if readErr == nil {
				_ = resp.Body.Close()

				if options.ShouldRectify(ctx, respBody) {
					options.Observe(ExchangeNotice{
						Platform:           options.Platform,
						AccountID:          options.AccountID,
						AccountName:        options.AccountName,
						UpstreamStatusCode: resp.StatusCode,
						UpstreamRequestID:  resp.Header.Get("x-request-id"),
						UpstreamURL:        options.SafeURL(upstreamReq.URL.String()),
						Kind:               "signature_error",
						Message:            options.ErrorMessage(respBody),
						Detail:             options.Detail(respBody),
					})

					looksLikeToolSignatureError := func(msg string) bool {
						m := strings.ToLower(msg)
						return strings.Contains(m, "tool_use") ||
							strings.Contains(m, "tool_result") ||
							strings.Contains(m, "functioncall") ||
							strings.Contains(m, "function_call") ||
							strings.Contains(m, "functionresponse") ||
							strings.Contains(m, "function_response")
					}

					if time.Since(retryStart) >= options.MaxElapsed {
						resp.Body = io.NopCloser(bytes.NewReader(respBody))
						break
					}
					logger.LegacyPrintf("service.gateway", "[warn] Account %d: thinking blocks have invalid signature, retrying with filtered blocks", options.AccountID)

					filteredBody := options.FilterThinking(body)
					retryCtx, releaseRetryCtx := options.Context(ctx, options.Stream)
					retryReq, retryWireBody, buildErr := options.Build(retryCtx, filteredBody)
					releaseRetryCtx()
					if buildErr == nil {
						retryResp, retryErr := options.Do(retryReq)
						if retryErr == nil {
							if retryResp.StatusCode < 400 {

								lastWireBody = retryWireBody
								if err := options.ReplaceBody(retryWireBody); err != nil {
									_ = retryResp.Body.Close()
									return nil, lastWireBody, err
								}
								logger.LegacyPrintf("service.gateway", "Account %d: thinking block retry succeeded (blocks downgraded)", options.AccountID)
								resp = retryResp
								break
							}

							retryRespBody, retryReadErr := options.ReadErrorBody(retryResp)
							_ = retryResp.Body.Close()
							if retryReadErr == nil && retryResp.StatusCode == 400 && options.IsSignatureError(ctx, retryRespBody) {
								options.Observe(ExchangeNotice{
									Platform:           options.Platform,
									AccountID:          options.AccountID,
									AccountName:        options.AccountName,
									UpstreamStatusCode: retryResp.StatusCode,
									UpstreamRequestID:  retryResp.Header.Get("x-request-id"),
									UpstreamURL:        options.SafeURL(retryReq.URL.String()),
									Kind:               "signature_retry_thinking",
									Message:            options.ErrorMessage(retryRespBody),
									Detail:             options.Detail(retryRespBody),
								})
								msg2 := options.ErrorMessage(retryRespBody)
								if looksLikeToolSignatureError(msg2) && time.Since(retryStart) < options.MaxElapsed {
									logger.LegacyPrintf("service.gateway", "Account %d: signature retry still failing and looks tool-related, retrying with tool blocks downgraded", options.AccountID)
									filteredBody2 := options.FilterTools(body)
									retryCtx2, releaseRetryCtx2 := options.Context(ctx, options.Stream)
									retryReq2, retryWireBody2, buildErr2 := options.Build(retryCtx2, filteredBody2)
									releaseRetryCtx2()
									if buildErr2 == nil {
										retryResp2, retryErr2 := options.Do(retryReq2)
										if retryErr2 == nil {
											if retryResp2.StatusCode < 400 {

												lastWireBody = retryWireBody2
												if err := options.ReplaceBody(retryWireBody2); err != nil {
													_ = retryResp2.Body.Close()
													return nil, lastWireBody, err
												}
											}
											resp = retryResp2
											break
										}
										if retryResp2 != nil && retryResp2.Body != nil {
											_ = retryResp2.Body.Close()
										}
										options.Observe(ExchangeNotice{
											Platform:           options.Platform,
											AccountID:          options.AccountID,
											AccountName:        options.AccountName,
											UpstreamStatusCode: 0,
											UpstreamURL:        options.SafeURL(retryReq2.URL.String()),
											Kind:               "signature_retry_tools_request_error",
											Message:            options.Sanitize(retryErr2.Error()),
										})
										logger.LegacyPrintf("service.gateway", "Account %d: tool-downgrade signature retry failed: %v", options.AccountID, retryErr2)
									} else {
										logger.LegacyPrintf("service.gateway", "Account %d: tool-downgrade signature retry build failed: %v", options.AccountID, buildErr2)
									}
								}
							}

							resp = &http.Response{
								StatusCode: retryResp.StatusCode,
								Header:     retryResp.Header.Clone(),
								Body:       io.NopCloser(bytes.NewReader(retryRespBody)),
							}
							break
						}
						if retryResp != nil && retryResp.Body != nil {
							_ = retryResp.Body.Close()
						}
						logger.LegacyPrintf("service.gateway", "Account %d: signature error retry failed: %v", options.AccountID, retryErr)
					} else {
						logger.LegacyPrintf("service.gateway", "Account %d: signature error retry build request failed: %v", options.AccountID, buildErr)
					}

					resp.Body = io.NopCloser(bytes.NewReader(respBody))
					break
				}

				errMsg := options.ErrorMessage(respBody)
				if options.IsBudgetError(errMsg) && options.BudgetEnabled(ctx) {
					options.Observe(ExchangeNotice{
						Platform:           options.Platform,
						AccountID:          options.AccountID,
						AccountName:        options.AccountName,
						UpstreamStatusCode: resp.StatusCode,
						UpstreamRequestID:  resp.Header.Get("x-request-id"),
						UpstreamURL:        options.SafeURL(upstreamReq.URL.String()),
						Kind:               "budget_constraint_error",
						Message:            errMsg,
						Detail:             options.Detail(respBody),
					})

					rectifiedBody, applied := options.RectifyBudget(body)
					if applied && time.Since(retryStart) < options.MaxElapsed {
						logger.LegacyPrintf("service.gateway", "Account %d: detected budget_tokens constraint error, retrying with rectified budget (budget_tokens=%d, max_tokens=%d)", options.AccountID, options.BudgetTokens, options.BudgetMaxTokens)
						budgetRetryCtx, releaseBudgetRetryCtx := options.Context(ctx, options.Stream)
						budgetRetryReq, budgetWireBody, buildErr := options.Build(budgetRetryCtx, rectifiedBody)
						releaseBudgetRetryCtx()
						if buildErr == nil {
							budgetRetryResp, retryErr := options.Do(budgetRetryReq)
							if retryErr == nil {
								if budgetRetryResp.StatusCode < 400 {

									lastWireBody = budgetWireBody
									if err := options.ReplaceBody(budgetWireBody); err != nil {
										_ = budgetRetryResp.Body.Close()
										return nil, lastWireBody, err
									}
								}
								resp = budgetRetryResp
								break
							}
							if budgetRetryResp != nil && budgetRetryResp.Body != nil {
								_ = budgetRetryResp.Body.Close()
							}
							logger.LegacyPrintf("service.gateway", "Account %d: budget rectifier retry failed: %v", options.AccountID, retryErr)
						} else {
							logger.LegacyPrintf("service.gateway", "Account %d: budget rectifier retry build failed: %v", options.AccountID, buildErr)
						}
					}
				}

				resp.Body = io.NopCloser(bytes.NewReader(respBody))
			}
		}

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
					Kind:               "retry",
					Message:            options.ErrorMessage(respBody),
					Detail:             options.Detail(respBody),
				})
				logger.LegacyPrintf("service.gateway", "Account %d: upstream error %d, retry %d/%d after %v (elapsed=%v/%v)",
					options.AccountID, resp.StatusCode, attempt, options.MaxAttempts, delay, elapsed, options.MaxElapsed)
				if err := upstream.WaitContext(ctx, delay); err != nil {
					return nil, lastWireBody, err
				}
				continue
			}

			break
		}

		if resp.StatusCode < 400 && options.DebugHeaders {
			logger.LegacyPrintf("service.gateway", "[DEBUG] Gemini API Response Headers for account %d:", options.AccountID)
			for k, v := range resp.Header {
				logger.LegacyPrintf("service.gateway", "[DEBUG]   %s: %v", k, v)
			}
		}
		break
	}

	return resp, lastWireBody, nil
}

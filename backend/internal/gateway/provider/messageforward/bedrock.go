package messageforward

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
)

// forwardBedrock 转发请求到 AWS Bedrock
func (r *Runtime) bedrock(
	ctx context.Context,
	output HTTPBoundary, state *AttemptState,
	account *gatewayprovider.ExecutionAccount,
	parsed *requeststate.ParsedRequest,
	startTime time.Time,
) (*forward.Result, error) {
	reqModel := parsed.Model
	reqStream := parsed.Stream
	body := parsed.Body.Bytes()

	route, err := gatewayprovider.ExecutionModelPolicy(account).BedrockRoute(reqModel)
	if err != nil {
		logging.LegacyPrintf("service.gateway", "[Bedrock] %s", bedrock.BedrockRoutingDiagnostic(err))
		return nil, err
	}
	region, mappedModel := route.SourceRegion, route.ModelID
	if mappedModel != reqModel {
		logging.LegacyPrintf("service.gateway", "[Bedrock] Model mapping: %s -> %s (account: %s)", reqModel, mappedModel, account.Record.Name)
	}

	betaHeader := ""
	if output.RequestPresent() {
		betaHeader = output.RequestHeaders().Get("anthropic-beta")
	}

	// 准备请求体（注入 anthropic_version/anthropic_beta，移除 Bedrock 不支持的字段，清理 cache_control）
	betaTokens, err := r.bedrockBetaTokens(ctx, account, betaHeader, body, mappedModel)
	if err != nil {
		return nil, err
	}

	bedrockBody, err := bedrock.PrepareBedrockRequestBodyWithTokens(body, mappedModel, betaTokens, false)
	if err != nil {
		return nil, fmt.Errorf("prepare bedrock request body: %w", err)
	}

	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}

	logging.LegacyPrintf("service.gateway", "[Bedrock] 命中 Bedrock 分支: account=%d name=%s model=%s->%s stream=%v",
		account.Record.ID, account.Record.Name, reqModel, mappedModel, reqStream)

	// 根据账号类型选择认证方式
	var signer *bedrock.BedrockSigner
	var bedrockAPIKey string
	if account.View().IsBedrockAPIKey() {
		bedrockAPIKey = account.View().GetCredential("api_key")
		if bedrockAPIKey == "" {
			return nil, fmt.Errorf("api_key not found in bedrock credentials")
		}
	} else {
		signer, err = accountprovider.NewBedrockSignerFromAccount(gatewayprovider.ExecutionRecord(account))
		if err != nil {
			return nil, fmt.Errorf("create bedrock signer: %w", err)
		}
	}

	options, policy := r.bedrockOptions(ctx, output, state, account, mappedModel, region, reqStream, signer, bedrockAPIKey, proxyURL)
	streamOptions := bedrock.StreamOptions{AccountID: account.Record.ID}
	if r.options.StreamInterval > 0 {
		streamOptions.Interval = r.options.StreamInterval
	}
	if r.dependencies.Health != nil {
		streamOptions.OnTimeout = func(ctx context.Context, model string) {
			r.dependencies.Health.Core.HandleStreamTimeout(ctx, gatewayprovider.ExecutionRecord(account), model)

		}
	}
	readLimit := r.options.ResponseReadLimit
	hadHTTPError := false
	var errorResult *forward.Result
	target := &bedrock.Target{AccountID: account.Record.ID, Request: options, Retry: policy, Stream: streamOptions, StartedAt: startTime, Enter: r.dependencies.Enter, Accepted: parsed.OnUpstreamAccepted, ReadBody: func(r io.Reader) ([]byte, error) {
		return output.ReadResponseBody(r, readLimit, MessagesBody)
	}, HTTPError: func(ctx context.Context, resp *http.Response) (upstream.AttemptResult, error) {
		hadHTTPError = true
		var err error
		errorResult, err = r.bedrockError(ctx, resp, output, state, account, mappedModel)
		return upstream.AttemptResult{}, err
	}}
	result, err := (bedrock.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocol.ProtocolAnthropicMessages, Body: bedrockBody, Stream: reqStream, ResponseModel: reqModel, Target: target}, output.Sink())
	if hadHTTPError {
		return errorResult, err
	}
	if err != nil {
		return nil, err
	}
	message := forward.MessagesFromAttempt(result)
	converted := (*forward.Result)(message)
	converted.UpstreamHeaders = result.UpstreamHeaders
	return converted, nil
}

// handleBedrockUpstreamErrors 处理 Bedrock 上游 4xx/5xx 错误（failover + 错误响应）
func (r *Runtime) bedrockError(
	ctx context.Context,
	resp *http.Response,
	output HTTPBoundary, state *AttemptState,
	account *gatewayprovider.ExecutionAccount,
	mappedModel string,
) (*forward.Result, error) {
	// 同账号重试耗尽后判断是否切换账号。
	if forward.ShouldRetry(account.View().IsOAuth(), resp.StatusCode) {
		if forward.ShouldFailover(resp.StatusCode) {
			respBody, _ := r.readErrorBody(resp)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(respBody))

			logging.LegacyPrintf("service.gateway", "[Bedrock] Upstream error (retry exhausted, failover): Account=%d(%s) Status=%d Body=%s",
				account.Record.ID, account.Record.Name, resp.StatusCode, logredact.TruncateUTF8(string(respBody), 1000))

			decision := r.retryHealth(ctx, resp, account, mappedModel)
			if decision.ShouldReturnGenericError() {
				return r.handleError(ctx, output, state, account, resp, false, mappedModel)
			}
			output.Observe(forward.Notice{
				Platform:           account.Record.Platform,
				AccountID:          account.Record.ID,
				AccountName:        account.Record.Name,
				UpstreamStatusCode: resp.StatusCode,
				Kind:               "retry_exhausted_failover",
				Message:            upstream.ExtractErrorMessage(respBody),
			})
			return nil, &forward.UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
			}
		}
		return r.handleError(ctx, output, state, account, resp, true, mappedModel)
	}

	// 无法在当前账号重试时，保留原切号裁决。
	if forward.ShouldFailover(resp.StatusCode) {
		respBody, _ := r.readErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		decision := r.failoverHealth(ctx, resp, account, mappedModel)
		if decision.ShouldReturnGenericError() {
			return r.handleError(ctx, output, state, account, resp, false, mappedModel)
		}
		output.Observe(forward.Notice{
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			Kind:               "failover",
			Message:            upstream.ExtractErrorMessage(respBody),
		})
		return nil, &forward.UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           respBody,
			RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
		}
	}

	// 其余错误按原协议输出。
	return r.handleError(ctx, output, state, account, resp, false, mappedModel)
}

func (r *Runtime) bedrockOptions(ctx context.Context, output HTTPBoundary, state *AttemptState, account *gatewayprovider.ExecutionAccount, modelID, region string, stream bool, signer *bedrock.BedrockSigner, apiKey, proxyURL string) (bedrock.RequestOptions, bedrock.RetryPolicy) {
	options := bedrock.RequestOptions{ModelID: modelID, Region: region, Stream: stream, Signer: signer, APIKey: apiKey, APIKeyMode: account.View().IsBedrockAPIKey(), Do: func(req *http.Request) (*http.Response, error) {
		return r.dependencies.Transport.DoWithTLS(req, proxyURL, account.Record.ID, account.Record.Concurrency, nil)
	}}
	policy := bedrock.RetryPolicy{MaxAttempts: maxRetryAttempts, MaxElapsed: maxRetryElapsed, Delay: forward.RetryDelay, ShouldRetry: func(status int) bool { return forward.ShouldRetry(account.View().IsOAuth(), status) }, ReadErrorBody: r.readErrorBody, TransportError: func(err error, url string) error {
		return r.transportError(ctx, output, account, err, forward.Notice{UpstreamURL: logredact.SafeUpstreamURL(url)})
	}, ObserveRetry: func(resp *http.Response, body []byte, url string, attempt int, delay time.Duration) {
		detail := ""
		if r.options.LogErrorBody {
			detail = logredact.TruncateUTF8(string(body), r.options.LogErrorBodyMaxBytes)
		}
		output.Observe(forward.Notice{Platform: account.Record.Platform, AccountID: account.Record.ID, AccountName: account.Record.Name, UpstreamStatusCode: resp.StatusCode, UpstreamURL: logredact.SafeUpstreamURL(url), Kind: "retry", Message: upstream.ExtractErrorMessage(body), Detail: detail})
		logging.LegacyPrintf("service.gateway", "[Bedrock] account %d: upstream error %d, retry %d/%d after %v", account.Record.ID, resp.StatusCode, attempt, maxRetryAttempts, delay)
	}}
	return options, policy
}

// bedrockBetaTokens 保留原 Header 校验、平台变换、最终 token 再校验的顺序。
func (r *Runtime) bedrockBetaTokens(ctx context.Context, target *gatewayprovider.ExecutionAccount, header string, body []byte, model string) ([]string, error) {
	policy := r.evaluateBeta(ctx, target, header, model)
	if policy.BlockErr != nil {
		return nil, policy.BlockErr
	}
	tokens := bedrock.ResolveBedrockBetaTokens(header, body, model)
	if err := r.checkBetaTokens(ctx, tokens, target, model); err != nil {
		return nil, err
	}
	return anthropic.FilterBetaTokens(tokens, policy.FilterSet), nil
}

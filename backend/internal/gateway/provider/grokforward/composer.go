package grokforward

import (
	"context"
	"fmt"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// DescribeImage 保留单张辅助请求、错误资格和描述用量，不绑定主会话缓存身份。
func DescribeImage(ctx context.Context, p Ports, o Options, in Input, imageURL string, index int) (string, protocolopenai.ForwardUsage, error) {

	body, err := o.Codec.BuildGrokComposerImageDescriptionBody(imageURL, index)
	if err != nil {
		return "", protocolopenai.ForwardUsage{}, err
	}
	upstreamCtx, releaseUpstreamCtx := p.Detach(ctx)
	// 图片描述探测是辅助请求而非会话轮次，不能绑定调用方的 Grok 提示缓存身份。
	upstreamReq, err := p.Build(upstreamCtx, body, "", false)
	releaseUpstreamCtx()
	if err != nil {
		return "", protocolopenai.ForwardUsage{}, fmt.Errorf("build grok composer image bridge request: %w", err)
	}
	p.ResolveProxy()
	var description string
	var usage protocolopenai.ForwardUsage
	target := &grok.ResponsesTarget{
		AccountID:     in.AccountID,
		Model:         ComposerVisionModel,
		Enter:         o.Enter,
		PassRawStream: true,
		Exchange: grok.ResponsesExchange{
			SingleExchange: true,
			Build:          func([]byte) (*http.Request, error) { return upstreamReq, nil },
			Do: func(req *http.Request) (*http.Response, error) {
				return p.Do(req)
			},
			ReadError: p.ReadError,
			AfterExchange: func(err error) error {
				if err != nil {
					return p.TransportError(ctx, err)
				}
				return nil
			},
		},
		BeforeResponse: func(resp *http.Response, _ []byte) (bool, error) {
			if resp.StatusCode >= 400 {
				respBody := p.ReadError(resp)
				upstreamMsg := p.ErrorMessage(respBody)
				if upstreamMsg == "" {
					upstreamMsg = fmt.Sprintf("xAI image bridge upstream returned status %d", resp.StatusCode)
				}
				decision := p.Health(ctx, resp.StatusCode, resp.Header, respBody, ComposerVisionModel, false)
				kind := "http_error"
				if decision.Failover {
					kind = "failover"
				}
				p.Observe(Notice{
					Platform:           in.Platform,
					AccountID:          in.AccountID,
					AccountName:        in.AccountName,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
					Kind:               kind,
					Message:            upstreamMsg,
				})
				if decision.Generic {
					return true, fmt.Errorf("grok composer image bridge upstream gateway error")
				}
				if kind == "failover" {
					retry := p.RetryMetadata(resp.StatusCode, respBody)
					return true, p.Failure(Failure{
						StatusCode:               resp.StatusCode,
						ResponseBody:             respBody,
						ResponseHeaders:          resp.Header.Clone(),
						RetryableOnSameAccount:   retry.Retryable || decision.RetrySameAccount,
						RequestScopedTransient:   retry.Retryable && resp.StatusCode == http.StatusTooManyRequests,
						SameAccountRetryDelay:    retry.Delay,
						SameAccountRetryDeadline: retry.Deadline,
						SameAccountRetryMax:      retry.Max,
					})
				}
				return true, fmt.Errorf("grok composer image bridge upstream error: %s", upstreamMsg)
			}
			p.ObserveSuccess(ctx, resp.Header, resp.StatusCode, ComposerVisionModel)
			return false, nil
		},
		ReadResponse: func(resp *http.Response, _ upstream.AttemptInput, _ upstream.OutputSink) (upstream.ResponsesObservation, error) {
			data, readErr := p.ReadBody(resp)
			if readErr != nil {
				return upstream.ResponsesObservation{}, fmt.Errorf("read grok composer image bridge response: %w", readErr)
			}
			var decodeErr error
			description, usage, decodeErr = grok.DecodeComposerDescription(data)
			return upstream.ResponsesObservation{Usage: &usage, HasUsage: p.HasTokens(&usage), Served: description != ""}, decodeErr
		},
	}
	_, err = (grok.ResponsesExecutor{}).Execute(upstreamCtx, upstream.AttemptInput{
		Protocol: protocol.ProtocolOpenAIResponses,
		Body:     body,
		Target:   target,
	}, nil)
	return description, usage, err

}

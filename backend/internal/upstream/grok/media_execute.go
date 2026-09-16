// 媒体单次执行持有 HTTP 响应，账号和任务/资金规则通过调用方组合。
package grok

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type MediaTarget struct {
	AccountID      int64
	Endpoint       GrokMediaEndpoint
	Request        *http.Request `json:"-"`
	StartedAt      time.Time
	Do             func(*http.Request) (*http.Response, error)
	Enter          func() (func(), error)
	AfterExchange  func(time.Duration, error) error
	BeforeResponse func(*http.Response) (bool, error)
	ReadBody       func(io.Reader) ([]byte, error)
	CountImages    func([]byte) int
	TransformBody  func([]byte) []byte
	CopyHeaders    func(http.Header, http.Header)
}

func (t *MediaTarget) TargetID() int64  { return t.AccountID }
func (*MediaTarget) String() string     { return "grok media target" }
func (t *MediaTarget) GoString() string { return t.String() }

// MissingImageOutput 只描述供应商成功报文缺少实际图片，故障转移由调用者决定。
type MissingImageOutput struct {
	Body    []byte
	Headers http.Header
}

func (*MissingImageOutput) Error() string { return "xAI upstream returned no image output" }

type MediaExecutor struct{}

func (MediaExecutor) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (result upstream.AttemptResult, failure error) {
	target, ok := input.Target.(*MediaTarget)
	if !ok || target == nil || target.Request == nil || target.Do == nil || target.ReadBody == nil {
		return result, errors.New("grok media target is not configured")
	}
	switch input.Protocol {
	case protocol.ProtocolImagesGenerations, protocol.ProtocolImagesEdits, protocol.ProtocolVideosGenerations, protocol.ProtocolVideosEdits, protocol.ProtocolVideosExtensions:
	default:
		return result, errors.New("unsupported grok media protocol")
	}
	if target.Enter != nil {
		done, err := target.Enter()
		if err != nil {
			return result, err
		}
		defer done()
	}
	started := time.Now()
	resp, err := target.Do(target.Request)
	if target.AfterExchange != nil {
		err = target.AfterExchange(time.Since(started), err)
	}
	if err != nil {
		return result, err
	}
	defer func() { _ = resp.Body.Close() }()
	if target.BeforeResponse != nil {
		handled, err := target.BeforeResponse(resp)
		if handled || err != nil {
			return result, err
		}
	}
	data, err := target.ReadBody(resp.Body)
	if err != nil {
		return result, err
	}
	if target.Endpoint == GrokMediaEndpointImagesGenerations || target.Endpoint == GrokMediaEndpointImagesEdits {
		result.ObservedImages = target.CountImages(data)
		if result.ObservedImages <= 0 {
			return result, &MissingImageOutput{Body: data, Headers: resp.Header.Clone()}
		}
	}
	if target.TransformBody != nil {
		data = target.TransformBody(data)
	}
	if sink != nil {
		output := upstream.NewOutputContext(sink)
		if target.CopyHeaders != nil {
			target.CopyHeaders(output.Writer.Header(), resp.Header)
		}
		ct := strings.TrimSpace(resp.Header.Get("Content-Type"))
		if ct == "" {
			ct = "application/json"
		}
		output.NextEvent(len(data) > 0, true)
		output.Data(resp.StatusCode, ct, data)
		result.ClientDisconnect = output.Err() != nil
	}
	result.RequestID = firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id"))
	result.UpstreamHeaders = resp.Header
	result.MediaBody = data
	result.Served = len(data) > 0
	result.Duration = time.Since(target.StartedAt)
	return result, nil
}

// BuildMediaRequest 保持 CLI 头先于 Content-Type、账号覆写最后应用的原顺序。
func BuildMediaRequest(ctx context.Context, endpoint GrokMediaEndpoint, targetURL, token, contentType string, body []byte, cliHeaders, overrides func(http.Header)) (*http.Request, error) {
	var reader io.Reader
	if endpoint.RequiresRequestBody() {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, endpoint.HTTPMethod(), targetURL, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if cliHeaders != nil {
		cliHeaders(req.Header)
	}
	if endpoint.RequiresRequestBody() {
		ct := strings.TrimSpace(contentType)
		if ct == "" {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
	}
	if overrides != nil {
		overrides(req.Header)
	}
	return req, nil
}

// 视频下载使用独立流式资源，不进入 JSON 执行接口，也不持有任务归属或计费状态。
package grok

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type VideoContentOptions struct {
	StatusURL, RequestID, Range string
	Token                       string `json:"-"`
	Context                     func(context.Context) context.Context
	ContentURL                  func() (string, error)
	ApplyHeaders                func(http.Header, string)
	Do                          func(*http.Request) (*http.Response, error)
	ReadStatus                  func(io.Reader) ([]byte, error)
	Latency                     func(time.Duration)
	TransportError              func(error) error
	HTTPError                   func(*http.Response, string) error
	Enter                       func() (func(), error)
}

func (VideoContentOptions) String() string     { return "grok video content options" }
func (o VideoContentOptions) GoString() string { return o.String() }

type VideoContent struct {
	StatusBody    []byte      `json:"-"`
	Headers       http.Header `json:"-"`
	StatusCode    int
	ContentLength int64
	RequestID     string
	body          io.ReadCloser
	done          func()
	once          sync.Once
	closeErr      error
}

func (v *VideoContent) Read(data []byte) (int, error) { return v.body.Read(data) }
func (v *VideoContent) Close() error {
	if v == nil {
		return nil
	}
	v.once.Do(func() {
		v.closeErr = v.body.Close()
		if v.done != nil {
			v.done()
		}
	})
	return v.closeErr
}
func (*VideoContent) String() string     { return "grok video content stream" }
func (v *VideoContent) GoString() string { return v.String() }

// OpenVideoContent 先确认状态，再选择官方签名 URL 或认证 relay；仅在 Close 时释放下载活动。
func OpenVideoContent(ctx context.Context, options VideoContentOptions) (content *VideoContent, failure error) {
	var done func()
	if options.Enter != nil {
		var err error
		done, err = options.Enter()
		if err != nil {
			return nil, err
		}
	}
	defer func() {
		if content == nil && done != nil {
			done()
		}
	}()
	statusReq, err := http.NewRequestWithContext(options.Context(ctx), http.MethodGet, options.StatusURL, nil)
	if err != nil {
		return nil, err
	}
	statusReq.Header.Set("Authorization", "Bearer "+options.Token)
	statusReq.Header.Set("Accept", "application/json")
	options.ApplyHeaders(statusReq.Header, options.StatusURL)
	started := time.Now()
	latency := func() { options.Latency(time.Since(started)) }
	statusResp, err := options.Do(statusReq)
	if err != nil {
		latency()
		return nil, options.TransportError(err)
	}
	statusRequestID := firstNonEmpty(statusResp.Header.Get("x-request-id"), statusResp.Header.Get("xai-request-id"))
	if statusResp.StatusCode >= 300 {
		defer func() { _ = statusResp.Body.Close() }()
		latency()
		if statusResp.StatusCode < 400 {
			return nil, fmt.Errorf("grok media status redirect is not allowed")
		}
		return nil, options.HTTPError(statusResp, statusRequestID)
	}
	statusBody, err := options.ReadStatus(statusResp.Body)
	_ = statusResp.Body.Close()
	if err != nil {
		latency()
		return nil, err
	}
	contentURL, err := (MediaCodec{}).GrokMediaSignedVideoContentURL(statusBody, options.RequestID)
	if err != nil {
		latency()
		return nil, err
	}
	signedContent := contentURL != ""
	if !signedContent {
		contentURL, err = options.ContentURL()
		if err != nil {
			latency()
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(options.Context(ctx), http.MethodGet, contentURL, nil)
	if err != nil {
		latency()
		return nil, err
	}
	req.Header.Set("Accept", "*/*")
	if header := strings.TrimSpace(options.Range); header != "" {
		req.Header.Set("Range", header)
	}
	if !signedContent {
		req.Header.Set("Authorization", "Bearer "+options.Token)
		options.ApplyHeaders(req.Header, contentURL)
	}
	resp, err := options.Do(req)
	latency()
	if err != nil {
		return nil, options.TransportError(err)
	}
	requestID := firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id"), statusRequestID)
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("grok media signed content redirect is not allowed")
	}
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		defer func() { _ = resp.Body.Close() }()
		return nil, options.HTTPError(resp, requestID)
	}
	return &VideoContent{
		StatusBody:    statusBody,
		Headers:       resp.Header,
		StatusCode:    resp.StatusCode,
		ContentLength: resp.ContentLength,
		RequestID:     requestID,
		body:          resp.Body,
		done:          done,
	}, nil
}

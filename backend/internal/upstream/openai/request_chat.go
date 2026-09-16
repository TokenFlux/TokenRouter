// CC 请求发送只组合协议头与本次技术参数，不识别其它平台或持有账号。
package openai

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// CCRequestOptions 的平台专属 Header 通过外层已经选定的端口写入。
type CCRequestOptions struct {
	URL              string
	Token            string `json:"-"`
	Stream           bool
	Headers          http.Header `json:"-"`
	RequestContext   func(context.Context) (context.Context, context.CancelFunc)
	ObserveEndpoint  func()
	AllowHeader      func(string) bool
	PrepareTransport func(*http.Request)
	FinalizeHeaders  func(http.Header)
	Do               func(*http.Request) (*http.Response, error)
	TransportError   func(error) error
}

// SendChatRequest 保持取得 context、构造、释放及 Header 覆写的原先后顺序。
func SendChatRequest(ctx context.Context, body []byte, options CCRequestOptions) (*http.Response, error) {
	upstreamCtx, release := options.RequestContext(ctx)
	request, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, options.URL, bytes.NewReader(body))
	release()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	options.ObserveEndpoint()
	request = request.WithContext(upstream.WithHTTPUpstreamProfile(request.Context(), upstream.HTTPUpstreamProfileOpenAI))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+options.Token)
	if options.Stream {
		request.Header.Set("Accept", "text/event-stream")
	} else {
		request.Header.Set("Accept", "application/json")
	}
	for key, values := range options.Headers {
		if options.AllowHeader(strings.ToLower(key)) {
			for _, value := range values {
				request.Header.Add(key, value)
			}
		}
	}
	options.PrepareTransport(request)
	options.FinalizeHeaders(request.Header)
	response, err := options.Do(request)
	if err != nil {
		return nil, options.TransportError(err)
	}
	return response, nil
}

// String 避免调试输出展开请求凭据和透传 Header。
func (CCRequestOptions) String() string     { return "openai chat request options" }
func (o CCRequestOptions) GoString() string { return o.String() }

// 独立帧连接和媒体流由原生实现关闭；这里只装配技术投影，不访问业务实体。
package provider

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
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

func (o VideoContentOptions) String() string   { return "media VideoContentOptions" }
func (o VideoContentOptions) GoString() string { return o.String() }
func (o VideoContentOptions) native() grok.VideoContentOptions {
	return grok.VideoContentOptions{StatusURL: o.StatusURL, RequestID: o.RequestID, Range: o.Range, Token: o.Token, Context: o.Context, ContentURL: o.ContentURL, ApplyHeaders: o.ApplyHeaders, Do: o.Do, ReadStatus: o.ReadStatus, Latency: o.Latency, TransportError: o.TransportError, HTTPError: o.HTTPError, Enter: o.Enter}
}
func OpenVideoContent(ctx context.Context, options VideoContentOptions) (*grok.VideoContent, error) {
	return grok.OpenVideoContent(ctx, options.native())
}

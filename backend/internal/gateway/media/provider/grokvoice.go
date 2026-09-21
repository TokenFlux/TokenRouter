// 原生平台目标只在此技术 Adapter 装配；请求级重试与资金仍由 media/completion 拥有。
package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

type GrokVoiceOptions struct {
	AccountID                           int64
	Endpoint, BaseEndpoint, ContentType string
	Request                             *http.Request `json:"-"`
	Do                                  func(*http.Request) (*http.Response, error)
	Enter                               func() (func(), error)
	AfterExchange                       func(time.Duration, error) error
	BeforeResponse                      func(*http.Response) (bool, error)
	ReadBody                            func(io.Reader) ([]byte, error)
	CopyHeaders                         func(http.Header, http.Header)
}

// String 防止技术参数中的令牌或请求被默认日志展开。
func (o GrokVoiceOptions) String() string {
	return fmt.Sprintf("media GrokVoice account=%d", o.AccountID)
}
func (o GrokVoiceOptions) GoString() string { return o.String() }

type GrokVoice struct{ Options GrokVoiceOptions }

func (e GrokVoice) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (upstream.AttemptResult, error) {
	o := e.Options
	target := &grok.VoiceTarget{
		AccountID:      o.AccountID,
		Endpoint:       o.Endpoint,
		BaseEndpoint:   o.BaseEndpoint,
		ContentType:    o.ContentType,
		Request:        o.Request,
		Do:             o.Do,
		Enter:          o.Enter,
		AfterExchange:  o.AfterExchange,
		BeforeResponse: o.BeforeResponse,
		ReadBody:       o.ReadBody,
		CopyHeaders:    o.CopyHeaders,
	}
	input.Target = target
	return (grok.VoiceExecutor{}).Execute(ctx, input, sink)
}

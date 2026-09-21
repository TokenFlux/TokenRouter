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

type GrokMediaOptions struct {
	AccountID      int64
	Endpoint       grok.GrokMediaEndpoint
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

// String 防止技术参数中的令牌或请求被默认日志展开。
func (o GrokMediaOptions) String() string {
	return fmt.Sprintf("media GrokMedia account=%d", o.AccountID)
}
func (o GrokMediaOptions) GoString() string { return o.String() }

type GrokMedia struct{ Options GrokMediaOptions }

func (e GrokMedia) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (upstream.AttemptResult, error) {
	o := e.Options
	target := &grok.MediaTarget{
		AccountID:      o.AccountID,
		Endpoint:       o.Endpoint,
		Request:        o.Request,
		StartedAt:      o.StartedAt,
		Do:             o.Do,
		Enter:          o.Enter,
		AfterExchange:  o.AfterExchange,
		BeforeResponse: o.BeforeResponse,
		ReadBody:       o.ReadBody,
		CountImages:    o.CountImages,
		TransformBody:  o.TransformBody,
		CopyHeaders:    o.CopyHeaders,
	}
	input.Target = target
	return (grok.MediaExecutor{}).Execute(ctx, input, sink)
}

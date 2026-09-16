// 原生平台目标只在此技术 Adapter 装配；请求级重试与资金仍由 media/completion 拥有。
package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// AlphaSearchTarget 不暴露完整账号，准备好的请求禁止序列化或日志展开。
type AlphaSearchOptions struct {
	AccountID         int64
	Request           *http.Request `json:"-"`
	ResponsesFallback bool
	Model             string
	Enter             func() (func(), error)
	Do                func(*http.Request) (*http.Response, error)
	Latency           func(time.Duration)
	TransportError    func(error) error
	ReadBody          func(io.Reader) ([]byte, error)
	HTTPError         func(*http.Response, []byte) error
	UpdateQuota       func(http.Header)
	Headers           func(http.Header, http.Header)
}

// String 防止技术参数中的令牌或请求被默认日志展开。
func (o AlphaSearchOptions) String() string {
	return fmt.Sprintf("media AlphaSearch account=%d", o.AccountID)
}
func (o AlphaSearchOptions) GoString() string { return o.String() }

type AlphaSearch struct{ Options AlphaSearchOptions }

func (e AlphaSearch) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (upstream.AttemptResult, error) {
	o := e.Options
	target := &native.AlphaSearchTarget{
		AccountID:         o.AccountID,
		Request:           o.Request,
		ResponsesFallback: o.ResponsesFallback,
		Model:             o.Model,
		Enter:             o.Enter,
		Do:                o.Do,
		Latency:           o.Latency,
		TransportError:    o.TransportError,
		ReadBody:          o.ReadBody,
		HTTPError:         o.HTTPError,
		UpdateQuota:       o.UpdateQuota,
		Headers:           o.Headers,
	}
	input.Target = target
	return (native.AlphaSearchExecutor{}).Execute(ctx, input, sink)
}

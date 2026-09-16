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

type EmbeddingsOptions struct {
	AccountID             int64
	Model, URL, UserAgent string
	Token                 string      `json:"-"`
	ForwardHeaders        http.Header `json:"-"`
	StartedAt             time.Time
	RequestContext        func(context.Context) (context.Context, context.CancelFunc)
	Enter                 func() (func(), error)
	ApplyHeaders          func(http.Header)
	Do                    func(*http.Request) (*http.Response, error)
	TransportError        func(error) error
	ReadErrorBody         func(*http.Response) []byte
	HTTPError             func(*http.Response, []byte) error
	ReadBody              func(io.Reader) ([]byte, error)
	ReadFailure           func(error) error
	WriteHeaders          func(http.Header, http.Header)
}

// String 防止技术参数中的令牌或请求被默认日志展开。
func (o EmbeddingsOptions) String() string {
	return fmt.Sprintf("media Embeddings account=%d", o.AccountID)
}
func (o EmbeddingsOptions) GoString() string { return o.String() }

type Embeddings struct{ Options EmbeddingsOptions }

func (e Embeddings) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (upstream.AttemptResult, error) {
	o := e.Options
	target := &native.EmbeddingsTarget{
		AccountID:      o.AccountID,
		Model:          o.Model,
		URL:            o.URL,
		UserAgent:      o.UserAgent,
		Token:          o.Token,
		ForwardHeaders: o.ForwardHeaders,
		StartedAt:      o.StartedAt,
		RequestContext: o.RequestContext,
		Enter:          o.Enter,
		ApplyHeaders:   o.ApplyHeaders,
		Do:             o.Do,
		TransportError: o.TransportError,
		ReadErrorBody:  o.ReadErrorBody,
		HTTPError:      o.HTTPError,
		ReadBody:       o.ReadBody,
		ReadFailure:    o.ReadFailure,
		WriteHeaders:   o.WriteHeaders,
	}
	input.Target = target
	return (native.EmbeddingsExecutor{}).Execute(ctx, input, sink)
}

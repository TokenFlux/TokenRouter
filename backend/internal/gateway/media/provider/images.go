// 原生平台目标只在此技术 Adapter 装配；请求级重试与资金仍由 media/completion 拥有。
package provider

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// ImagesTarget 是已准备的单次技术目标，不能序列化请求中的凭据。
type ImagesOptions struct {
	AccountID                           int64
	OAuth                               bool
	Model, ResponseFormat, StreamPrefix string
	StartedAt                           time.Time
	Request                             *http.Request `json:"-"`
	Options                             native.ImageResponseOptions
	Enter                               func() (func(), error)
	Do                                  func(*http.Request) (*http.Response, error)
	TransportError                      func(error) error
	ReadErrorBody                       func(*http.Response) []byte
	RedactErrorBody                     func([]byte) []byte
	HTTPError                           func(*http.Response, []byte) error
	ResponseError                       func(*http.Response, int, error) error
}

// String 防止技术参数中的令牌或请求被默认日志展开。
func (o ImagesOptions) String() string   { return fmt.Sprintf("media Images account=%d", o.AccountID) }
func (o ImagesOptions) GoString() string { return o.String() }

type Images struct{ Options ImagesOptions }

func (e Images) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (upstream.AttemptResult, error) {
	o := e.Options
	target := &native.ImagesTarget{
		AccountID:       o.AccountID,
		OAuth:           o.OAuth,
		Model:           o.Model,
		ResponseFormat:  o.ResponseFormat,
		StreamPrefix:    o.StreamPrefix,
		StartedAt:       o.StartedAt,
		Request:         o.Request,
		Options:         o.Options,
		Enter:           o.Enter,
		Do:              o.Do,
		TransportError:  o.TransportError,
		ReadErrorBody:   o.ReadErrorBody,
		RedactErrorBody: o.RedactErrorBody,
		HTTPError:       o.HTTPError,
		ResponseError:   o.ResponseError,
	}
	input.Target = target
	return (native.ImagesExecutor{}).Execute(ctx, input, sink)
}

// 独立帧连接和媒体流由原生实现关闭；这里只装配技术投影，不访问业务实体。
package provider

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

type RealtimeOptions struct {
	BaseURL, Model           string
	Token                    string `json:"-"`
	CLIHeaders, ApplyHeaders func(http.Header)
	Dial                     func(context.Context, string, http.Header) (upstream.FrameConn, int, error)
	Enter                    func() (func(), error)
}

func (o RealtimeOptions) String() string   { return "media RealtimeOptions" }
func (o RealtimeOptions) GoString() string { return o.String() }
func (o RealtimeOptions) native() grok.RealtimeDialOptions {
	return grok.RealtimeDialOptions{BaseURL: o.BaseURL, Model: o.Model, Token: o.Token, CLIHeaders: o.CLIHeaders, ApplyHeaders: o.ApplyHeaders, Dial: o.Dial, Enter: o.Enter}
}
func DialRealtime(ctx context.Context, options RealtimeOptions) (*grok.RealtimeSession, error) {
	return grok.DialRealtime(ctx, options.native())
}
func ProbeRealtime(ctx context.Context, options RealtimeOptions) error {
	return grok.ProbeRealtime(ctx, options.native())
}

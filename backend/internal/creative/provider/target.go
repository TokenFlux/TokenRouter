// Target 是一次任务尝试的受控执行目标，不保存共享缓存、账号仓储或网关服务。
package provider

import (
	"context"
	"net/http"

	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

type OpenAIOptions struct {
	Token        func(context.Context) (string, error)
	URL          func(string) (string, error)
	Prepare      func(*http.Request) *http.Request
	AuthHeaders  func(context.Context, string) (http.Header, error)
	ApplyHeaders func(http.Header)
	Do           func(*http.Request) (*http.Response, error)
}
type GrokOptions struct {
	OAuth        bool
	Token        func(context.Context) (string, error)
	URL          func(nativegrok.GrokMediaEndpoint) (string, error)
	Prepare      func(*http.Request) *http.Request
	ApplyHeaders func(http.Header)
	Do           func(*http.Request) (*http.Response, error)
}
type Target struct {
	OpenAI *OpenAIOptions
	Grok   *GrokOptions
	Gemini func(string) gemininative.ImageOptions
}

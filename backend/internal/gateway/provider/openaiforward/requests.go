// Responses 请求构造统一承接目标选择与无状态归一化，保留两种入口的 Header 差异。
package openaiforward

import (
	"context"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"net/http"
	"net/url"
)

// RequestTargetOptions 只含本次凭据类别和按需目标读取，不持有账号或配置。
type RequestTargetOptions struct {
	OAuthTarget, APIKey  bool
	DefaultURL, CodexURL string
	BaseURL              func() string
	Validate             func(string) (string, error)
	FromBase             func(string) string
	AppendSuffix         func(string) string
}

func (o RequestTargetOptions) Resolve() (string, error) {
	target := o.DefaultURL
	if o.OAuthTarget {
		target = o.CodexURL
	} else if o.APIKey {
		if base := o.BaseURL(); base != "" {
			validated, err := o.Validate(base)
			if err != nil {
				return "", err
			}
			target = o.FromBase(validated)
		}
	}
	return o.AppendSuffix(target), nil
}

// BuildResponsesRequest 保留先观察端点、再归一化报文与构造 Header 的顺序。
func BuildResponsesRequest(ctx context.Context, body []byte, key string, target RequestTargetOptions, observe func(string), normalize func([]byte) []byte, options func(string) native.ResponsesRequestOptions) (*http.Request, error) {
	address, err := target.Resolve()
	if err != nil {
		return nil, err
	}
	if parsed, e := url.Parse(address); e == nil {
		observe(parsed.Path)
	}
	body = normalize(body)
	return native.BuildResponsesRequest(ctx, body, key, options(address))
}

// BuildPassthroughRequest 保留透传入口独有的超时 Header 和 originator 策略。
func BuildPassthroughRequest(ctx context.Context, body []byte, target RequestTargetOptions, normalize func([]byte) []byte, options func(string) native.PassthroughRequestOptions) (*http.Request, error) {
	address, err := target.Resolve()
	if err != nil {
		return nil, err
	}
	body = normalize(body)
	return native.BuildPassthroughRequest(ctx, body, options(address))
}

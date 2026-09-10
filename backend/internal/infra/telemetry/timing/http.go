package timing

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type dependencyModuleKey struct{}

type timingRoundTripper struct {
	base http.RoundTripper
}

// WithDependencyModule 覆盖出站请求使用的安全模块名。
func WithDependencyModule(ctx context.Context, module string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	module = strings.TrimPrefix(normalizeMetricName(module), dependencyPrefix)
	if module == "" {
		return ctx
	}
	return context.WithValue(ctx, dependencyModuleKey{}, module)
}

// WrapRoundTripper 为启用采集的请求记录出站响应头延迟。
func WrapRoundTripper(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*timingRoundTripper); ok {
		return base
	}
	return &timingRoundTripper{base: base}
}

// InstrumentClient 返回装配了耗时采集 Transport 的客户端浅拷贝。
func InstrumentClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	copyClient.Transport = WrapRoundTripper(copyClient.Transport)
	return &copyClient
}

// Do 在不改变客户端 Transport 类型的前提下记录响应头延迟，适用于调用方需要检查或配置
// *http.Transport 的客户端。
func Do(client *http.Client, req *http.Request) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if req == nil || !Active(req.Context()) {
		//nolint:gosec // 此处仅透明执行调用方构造的请求，目标地址由各上游客户端负责校验。
		return client.Do(req)
	}
	startedAt := time.Now()
	//nolint:gosec // 此处仅透明执行调用方构造的请求，目标地址由各上游客户端负责校验。
	response, err := client.Do(req)
	RecordDependency(req.Context(), dependencyModule(req), startedAt, time.Now())
	return response, err
}

func (t *timingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || !Active(req.Context()) {
		return t.base.RoundTrip(req)
	}
	startedAt := time.Now()
	response, err := t.base.RoundTrip(req)
	RecordDependency(req.Context(), dependencyModule(req), startedAt, time.Now())
	return response, err
}

func dependencyModule(req *http.Request) string {
	if req != nil {
		if module, ok := req.Context().Value(dependencyModuleKey{}).(string); ok && module != "" {
			return module
		}
	}
	if req == nil || req.URL == nil {
		return "http"
	}
	host := strings.ToLower(req.URL.Hostname())
	switch {
	case strings.Contains(host, "github"):
		return "github"
	case strings.Contains(host, "openai"):
		return "openai"
	case strings.Contains(host, "anthropic"):
		return "anthropic"
	case strings.Contains(host, "generativelanguage") || strings.Contains(host, "gemini"):
		return "gemini"
	case strings.Contains(host, "cloudcode") || strings.Contains(host, "antigravity"):
		return "antigravity"
	case strings.Contains(host, "googleapis") || strings.Contains(host, "google"):
		return "google"
	case strings.Contains(host, "amazonaws") || strings.Contains(host, "cloudflarestorage") || strings.Contains(host, "s3"):
		return "s3"
	case strings.Contains(host, "stripe") || strings.Contains(host, "airwallex") || strings.Contains(host, "alipay") || strings.Contains(host, "wechatpay") || strings.Contains(host, "paypal"):
		return "payment"
	default:
		return "http"
	}
}

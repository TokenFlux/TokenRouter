package messageforward

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// 请求构造测试只提供 Header；若误用响应端口，嵌入的空接口会让测试失败。
type requestBoundaryFixture struct {
	HTTPBoundary
	Request *http.Request
}

func (b *requestBoundaryFixture) Present() bool        { return b != nil }
func (b *requestBoundaryFixture) RequestPresent() bool { return b != nil && b.Request != nil }
func (b *requestBoundaryFixture) RequestHeaders() http.Header {
	if !b.RequestPresent() {
		return http.Header{}
	}
	return b.Request.Header
}

type betaSettingsFixture struct {
	gateway.RuntimeSettingsStore
	values map[string]string
}

func (s betaSettingsFixture) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", settings.ErrSettingNotFound
}

func newBetaRuntime(values map[string]string) *gateway.RuntimeSettings {
	return gateway.NewRuntimeSettings(betaSettingsFixture{values: values}, settings.ErrSettingNotFound, func() *gateway.BetaPolicySettings {
		return provider.GatewayBetaPolicy(anthropic.DefaultBetaPolicySettings())
	})
}

// 每次读取返回独立映射，设置用例可以在主动失效后观察新值。
func (s betaSettingsFixture) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string)
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// imagesFixtureInputs 只提供图片执行实际使用的传输、账号存储和输出预算。
type imagesFixtureInputs struct {
	observer                       *provider.UpstreamHealth
	transport                      httpclient.UpstreamTransport
	store                          gatewayprovider.ExecutionAccountStore
	allowHTTP                      bool
	ImageStreamDataIntervalTimeout int
	ImageStreamKeepaliveInterval   int
}

func newImagesFixture(v imagesFixtureInputs) *OpenAIImagesExecutor {
	auxiliary := newAuxiliaryFixture(auxiliaryFixtureInputs{transport: v.transport, store: v.store, allowHTTP: v.allowHTTP, observer: v.observer})
	auxiliary.Output.Options.ImageStreamDataIntervalTimeout = v.ImageStreamDataIntervalTimeout
	auxiliary.Output.Options.ImageStreamKeepaliveInterval = v.ImageStreamKeepaliveInterval
	return &OpenAIImagesExecutor{Requests: auxiliary.Requests, Output: auxiliary.Output, Cooldown: &provider.ImageToolCooldown{Store: v.store}}
}

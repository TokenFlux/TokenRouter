package handler

import (
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
)

func openAIImageAdmissionOptions(cfg *config.Config) *gatewayhttp.OpenAIImageAdmissionOptions {
	if cfg == nil {
		return nil
	}
	value := cfg.Gateway.ImageConcurrency
	return &gatewayhttp.OpenAIImageAdmissionOptions{Enabled: value.Enabled, Limit: value.MaxConcurrentRequests, Wait: strings.TrimSpace(value.OverflowMode) == config.ImageConcurrencyOverflowModeWait, Timeout: time.Duration(value.WaitTimeoutSeconds) * time.Second, MaxWaiting: value.MaxWaitingRequests}
}

// httpResources 仅给剩余旧入口投影同一资源，不能复制 limiter 或并发状态。
func (h *OpenAIGatewayHandler) httpResources() *gatewayhttp.OpenAIHTTPResources {
	if h == nil {
		return nil
	}
	return &gatewayhttp.OpenAIHTTPResources{Concurrency: h.concurrencyHelper, Images: h.imageLimiter, ImageOptions: openAIImageAdmissionOptions(h.cfg)}
}
func (h *OpenAIGatewayHandler) httpDependencies() gatewayhttp.OpenAIDependencies {
	if h == nil {
		return gatewayhttp.OpenAIDependencies{}
	}
	return gatewayhttp.OpenAIDependencies{Handler: true, Gateway: h.gatewayService != nil, Funding: h.billingCacheService != nil, Keys: h.apiKeyService != nil, Concurrency: h.concurrencyHelper != nil && h.concurrencyHelper.Service() != nil}
}

package app

import (
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// provideOpenAIHTTPResources 由组合根构造一次，供文本、媒体、WS 与剩余兼容入口共享。
func provideOpenAIHTTPResources(concurrency *scheduler.ConcurrencyService, cfg *config.Config) *gatewayhttp.OpenAIHTTPResources {
	ping := time.Duration(0)
	imageOptions := openAIImageAdmissionOptions(cfg)
	if cfg != nil {
		ping = time.Duration(cfg.Concurrency.PingInterval) * time.Second

	}
	return &gatewayhttp.OpenAIHTTPResources{Concurrency: gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatComment, ping), Images: &scheduler.ImageConcurrencyLimiter{}, ImageOptions: imageOptions}
}

// openAIImageAdmissionOptions 仅投影配置，不创建限流实例。
func openAIImageAdmissionOptions(cfg *config.Config) *gatewayhttp.OpenAIImageAdmissionOptions {
	if cfg == nil {
		return nil
	}
	value := cfg.Gateway.ImageConcurrency
	return &gatewayhttp.OpenAIImageAdmissionOptions{Enabled: value.Enabled, Limit: value.MaxConcurrentRequests, Wait: strings.TrimSpace(value.OverflowMode) == config.ImageConcurrencyOverflowModeWait, Timeout: time.Duration(value.WaitTimeoutSeconds) * time.Second, MaxWaiting: value.MaxWaitingRequests}
}

package service

import (
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// compositeReadOptions 只为旧独立构造保留参数投影，规则由领域与 composite.Parse 唯一实现。
func (s *SettingService) compositeReadOptions() composite.ReadOptions {
	return composite.ReadOptions{OAuth: s.OAuthSettings(), Gateway: s.GatewayAdminRules(), Scheduler: s.SchedulerAdminDefaults(), DefaultBalance: func() float64 { return s.cfg.Default.UserBalance }, DefaultConcurrency: func() int { return s.cfg.Default.UserConcurrency }, Forwarded: func() runtimeconfig.ForwardedInput {
		value := runtimeconfig.ForwardedInput{ForwardedClientIPHeaders: []string{}}
		if s != nil && s.cfg != nil {
			current := s.cfg.ForwardedClientIPSettings()
			value.APIKeyACLTrustForwardedIP = current.TrustForwardedIP
			value.ForwardedClientIPHeaders = current.Headers
		}
		return value
	}, PublishModel: func(model string, enabled bool) {
		xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{DefaultText: model, EnableCrossClientMap: enabled})
	}}
}

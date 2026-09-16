// 用户提示词的规则与缓存唯一位于 gateway/promptpolicy，旧入口只转交。
package service

import (
	"context"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
)

type UserPromptReplacementConfig = promptpolicy.UserPromptReplacementConfig
type UserPromptReplacementRule = promptpolicy.UserPromptReplacementRule
type compiledUserPromptReplacementConfig = promptpolicy.CompiledConfig

var userPromptReplacementCache = promptpolicy.SharedCache()

const UserPromptReplacementTypeStatic = promptpolicy.UserPromptReplacementTypeStatic
const UserPromptReplacementTypeTimezoneName = promptpolicy.UserPromptReplacementTypeTimezoneName
const UserPromptReplacementTypeCurrentTime = promptpolicy.UserPromptReplacementTypeCurrentTime
const UserPromptReplacementScopeEnvironmentContext = promptpolicy.UserPromptReplacementScopeEnvironmentContext

func DefaultUserPromptReplacementConfig() *UserPromptReplacementConfig {
	return promptpolicy.DefaultUserPromptReplacementConfig()
}
func defaultUserPromptReplacementConfigJSON() string { return promptpolicy.DefaultConfigJSON() }
func parseUserPromptReplacementConfig(raw string) *UserPromptReplacementConfig {
	return promptpolicy.ParseConfig(raw, slog.Warn)
}
func userPromptReplacementConfigToRaw(cfg *UserPromptReplacementConfig) (string, error) {
	return promptpolicy.ConfigToRaw(cfg)
}
func UserPromptReplacementRuleTargetGroupOptions(pattern string) []int {
	return promptpolicy.UserPromptReplacementRuleTargetGroupOptions(pattern)
}

// BindUserPromptPolicy 在应用开放入口前绑定唯一运行门面。
func (s *SettingService) BindUserPromptPolicy(policy *promptpolicy.Service) {
	s.userPromptPolicy = policy
}
func (s *SettingService) promptPolicy() *promptpolicy.Service {
	if s == nil {
		return nil
	}
	if s.userPromptPolicy != nil {
		return s.userPromptPolicy
	}
	return promptpolicy.New(s.settingRepo, ErrSettingNotFound, slog.Warn)
}
func (s *SettingService) GetUserPromptReplacementConfig(ctx context.Context) (*UserPromptReplacementConfig, error) {
	return s.promptPolicy().GetUserPromptReplacementConfig(ctx)
}
func (s *SettingService) SetUserPromptReplacementConfig(ctx context.Context, cfg *UserPromptReplacementConfig) error {
	return s.promptPolicy().SetUserPromptReplacementConfig(ctx, cfg)
}
func (s *SettingService) ApplyUserPromptReplacementToBody(ctx context.Context, body []byte, protocol string) []byte {
	return s.promptPolicy().ApplyUserPromptReplacementToBody(ctx, body, protocol)
}

// ApplyUserPromptReplacement 将用户提示词替换规则应用到通用网关请求体。
func (s *GatewayService) ApplyUserPromptReplacement(ctx context.Context, body []byte, protocol string) []byte {
	if s == nil || s.settingService == nil {
		return body
	}
	return s.settingService.ApplyUserPromptReplacementToBody(ctx, body, protocol)
}

// ApplyUserPromptReplacement 将用户提示词替换规则应用到 OpenAI 网关请求体。
func (s *OpenAIGatewayService) ApplyUserPromptReplacement(ctx context.Context, body []byte, protocol string) []byte {
	if s == nil || s.settingService == nil {
		return body
	}
	return s.settingService.ApplyUserPromptReplacementToBody(ctx, body, protocol)
}

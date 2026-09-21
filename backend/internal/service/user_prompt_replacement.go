// 用户提示词的规则与缓存唯一位于 gateway/promptpolicy，旧入口只转交。
package service

import (
	"context"
)

// ApplyUserPromptReplacement 将用户提示词替换规则应用到通用网关请求体。
func (s *GatewayService) ApplyUserPromptReplacement(ctx context.Context, body []byte, protocol string) []byte {
	if s == nil || s.settingService == nil {
		return body
	}
	return s.settingService.Prompts.ApplyUserPromptReplacementToBody(ctx, body, protocol)
}

// ApplyUserPromptReplacement 将用户提示词替换规则应用到 OpenAI 网关请求体。
func (s *OpenAIGatewayService) ApplyUserPromptReplacement(ctx context.Context, body []byte, protocol string) []byte {
	if s == nil || s.settingService == nil {
		return body
	}
	return s.settingService.Prompts.ApplyUserPromptReplacementToBody(ctx, body, protocol)
}

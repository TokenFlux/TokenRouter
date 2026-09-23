package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// ParseOpenAICyberPolicyEvent 复用供应商识别规则，保留原正文截断和已观测用量。
func ParseOpenAICyberPolicyEvent(payload []byte, upstreamStatus int, usage *openai.ForwardUsage) *moderationflow.Mark {
	hit, code, message := upstreamopenai.DetectOpenAICyberPolicy(payload)
	if !hit {
		return nil
	}
	mark := &moderationflow.Mark{
		Code: code, Message: message,
		Body: logredact.TruncateUTF8(string(payload), 4096), UpstreamStatus: upstreamStatus,
	}
	if usage != nil {
		mark.UpstreamInTok = usage.InputTokens
		mark.UpstreamOutTok = usage.OutputTokens
	}
	return mark
}

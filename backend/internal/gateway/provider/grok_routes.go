package provider

import (
	"context"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// GrokRoutes 接收已投影的目标策略，在原位置读取动态默认地址。
type GrokRoutes struct {
	Validate    grok.BaseURLValidator
	DefaultMode func(context.Context) string
}

func (p GrokRoutes) Responses(target *ExecutionAccount, runtimeDefault bool) (string, error) {
	validate, err := accountprovider.GrokBaseURLValidator(ExecutionRecord(target), p.Validate)
	if err != nil {
		return "", err
	}
	base := accountprovider.GrokAccountBaseURL(ExecutionRecord(target))
	if runtimeDefault && p.DefaultMode != nil {
		fallback := GrokBaseURLForMode(p.DefaultMode(context.Background()))
		base = accountprovider.GrokAccountBaseURLOr(ExecutionRecord(target), fallback)
	}
	return grok.BuildResponsesURLWithValidator(base, validate)
}
func (p GrokRoutes) Chat(target *ExecutionAccount, runtimeDefault bool) (string, error) {
	validate, err := accountprovider.GrokBaseURLValidator(ExecutionRecord(target), p.Validate)
	if err != nil {
		return "", err
	}
	base := accountprovider.GrokAccountBaseURL(ExecutionRecord(target))
	if runtimeDefault && p.DefaultMode != nil {
		fallback := GrokBaseURLForMode(p.DefaultMode(context.Background()))
		base = accountprovider.GrokAccountBaseURLOr(ExecutionRecord(target), fallback)
	}
	return grok.BuildChatCompletionsURLWithValidator(base, validate)
}
func (p GrokRoutes) Media(target *ExecutionAccount, endpoint grok.GrokMediaEndpoint, requestID string) (string, error) {
	validate, err := accountprovider.GrokBaseURLValidator(ExecutionRecord(target), p.Validate)
	if err != nil {
		return "", err
	}
	return grok.BuildMediaEndpointURL(accountprovider.GrokAccountMediaBaseURL(ExecutionRecord(target)), endpoint, requestID, validate)
}
func (p GrokRoutes) Voice(target *ExecutionAccount, endpoint string) (string, error) {
	validate, err := accountprovider.GrokBaseURLValidator(ExecutionRecord(target), p.Validate)
	if err != nil {
		return "", err
	}
	return grok.BuildVoiceEndpointURL(accountprovider.GrokAccountMediaBaseURL(ExecutionRecord(target)), endpoint, validate)
}

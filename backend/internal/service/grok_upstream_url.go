package service

import (
	"context"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/config"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func buildGrokResponsesURL(account *gatewayprovider.ExecutionAccount, cfg *config.Config, settings ...*gatewayprovider.RuntimeReaders) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	baseURL := accountprovider.GrokAccountBaseURL(gatewayprovider.ExecutionRecord(account))
	if len(settings) > 0 && settings[0] != nil {
		fallback := gatewayprovider.GrokBaseURLForMode(settings[0].Gateway.GetGrokDefaultBaseURLMode(context.Background()))
		baseURL = accountprovider.GrokAccountBaseURLOr(gatewayprovider.ExecutionRecord(account), fallback)
	}
	return xai.BuildResponsesURLWithValidator(baseURL, validator)
}

func buildGrokChatCompletionsURL(account *gatewayprovider.ExecutionAccount, cfg *config.Config, settings ...*gatewayprovider.RuntimeReaders) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	baseURL := accountprovider.GrokAccountBaseURL(gatewayprovider.ExecutionRecord(account))
	if len(settings) > 0 && settings[0] != nil {
		fallback := gatewayprovider.GrokBaseURLForMode(settings[0].Gateway.GetGrokDefaultBaseURLMode(context.Background()))
		baseURL = accountprovider.GrokAccountBaseURLOr(gatewayprovider.ExecutionRecord(account), fallback)
	}
	return xai.BuildChatCompletionsURLWithValidator(baseURL, validator)
}

func buildGrokMediaURL(account *gatewayprovider.ExecutionAccount, cfg *config.Config, endpoint xai.GrokMediaEndpoint, requestID string) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	return xai.BuildMediaEndpointURL(accountprovider.GrokAccountMediaBaseURL(gatewayprovider.ExecutionRecord(account)), endpoint, requestID, validator)
}

func buildGrokVoiceURL(account *gatewayprovider.ExecutionAccount, cfg *config.Config, endpoint string) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	return xai.BuildVoiceEndpointURL(accountprovider.GrokAccountMediaBaseURL(gatewayprovider.ExecutionRecord(account)), endpoint, validator)
}

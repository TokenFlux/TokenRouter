package service

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/config"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func buildGrokResponsesURL(account *Account, cfg *config.Config, settings ...*gatewayprovider.RuntimeReaders) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	baseURL := account.GetGrokBaseURL()
	if len(settings) > 0 && settings[0] != nil {
		baseURL = account.GetGrokBaseURLOr(gatewayprovider.GrokBaseURLForMode(settings[0].Gateway.GetGrokDefaultBaseURLMode(context.Background())))
	}
	return xai.BuildResponsesURLWithValidator(baseURL, validator)
}

func buildGrokChatCompletionsURL(account *Account, cfg *config.Config, settings ...*gatewayprovider.RuntimeReaders) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	baseURL := account.GetGrokBaseURL()
	if len(settings) > 0 && settings[0] != nil {
		baseURL = account.GetGrokBaseURLOr(gatewayprovider.GrokBaseURLForMode(settings[0].Gateway.GetGrokDefaultBaseURLMode(context.Background())))
	}
	return xai.BuildChatCompletionsURLWithValidator(baseURL, validator)
}

func buildGrokMediaURL(account *Account, cfg *config.Config, endpoint xai.GrokMediaEndpoint, requestID string) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	return xai.BuildMediaEndpointURL(account.GetGrokMediaBaseURL(), endpoint, requestID, validator)
}

func buildGrokVoiceURL(account *Account, cfg *config.Config, endpoint string) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	return xai.BuildVoiceEndpointURL(account.GetGrokMediaBaseURL(), endpoint, validator)
}

package handler

import (
	"context"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
)

func (h *OpenAIGatewayHandler) ensureOpenAISessionIsolation(ctx context.Context, apiKey *apikey.APIKey, userID int64, source, sessionHash string) error {
	if h == nil || h.gatewayService == nil {
		return nil
	}
	return h.gatewayService.EnsureSessionIsolation(ctx, apiKey, userID, source, sessionHash)
}

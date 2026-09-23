package handler

import (
	"github.com/gin-gonic/gin"
)

// Responses handles OpenAI Responses API endpoint for Anthropic platform groups.
// POST /v1/responses
// This converts Responses API requests to Anthropic format, forwards to Anthropic
// upstream, and converts responses back to Responses format.
func (h *GatewayHandler) Responses(c *gin.Context) { h.NewCompatibleTextHTTPHandler().Responses(c) }

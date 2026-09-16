package handler

import (
	"time"

	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) Images(c *gin.Context) { h.MediaHTTPHandler().Images(c) }

func (h *OpenAIGatewayHandler) openAIImagesJSONKeepaliveInterval() time.Duration {
	if h.cfg == nil || h.cfg.Gateway.ImageNonstreamKeepaliveInterval <= 0 {
		return 0
	}
	return time.Duration(h.cfg.Gateway.ImageNonstreamKeepaliveInterval) * time.Second
}

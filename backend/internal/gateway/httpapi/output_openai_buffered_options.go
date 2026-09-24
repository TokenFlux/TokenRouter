package httpapi

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (p *OpenAIResponseOutput) BufferedOptions(c *gin.Context, logPrefix, requestID string) openai.CompatBufferedOptions {
	maxLineSize := openAIResponseDefaultMaxLineSize
	if p.Options.Configured && p.Options.MaxLineSize > 0 {
		maxLineSize = p.Options.MaxLineSize
	}
	return openai.CompatBufferedOptions{
		MaxLineSize: maxLineSize,
		StreamInterval: func() time.Duration {
			if p.Options.Configured && p.Options.StreamDataIntervalTimeout > 0 {
				return time.Duration(p.Options.StreamDataIntervalTimeout) * time.Second
			}
			return 0
		},
		RestoreToolNames: func(body []byte) []byte { return RestoreCodexToolNamesFromContext(c, body) },
		Observe:          func(body []byte, event string) { ObserveOpenAIServiceTierInContext(c, body, event) },
		Log: func(message string, err error, interval time.Duration) {
			if err != nil {
				logging.L().Warn(logPrefix+": "+message, zap.Error(err), zap.String("request_id", requestID))
				return
			}
			logging.L().Warn(logPrefix+": "+message, zap.String("request_id", requestID), zap.Duration("interval", interval))
		},
	}
}

// 缓冲读取只在原时点读取配置、恢复请求别名和记录诊断。
package service

import (
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (s *OpenAIGatewayService) nativeCompatBufferedOptions(c *gin.Context, logPrefix, requestID string) openai.CompatBufferedOptions {
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	return openai.CompatBufferedOptions{
		MaxLineSize: maxLineSize,
		StreamInterval: func() time.Duration {
			if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
				return time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
			}
			return 0
		},
		RestoreToolNames: func(body []byte) []byte { return restoreCodexToolNamesFromContext(c, body) },
		Observe:          func(body []byte, event string) { gatewayhttp.ObserveOpenAIServiceTierInContext(c, body, event) },
		Log: func(message string, err error, interval time.Duration) {
			if err != nil {
				logging.L().Warn(logPrefix+": "+message, zap.Error(err), zap.String("request_id", requestID))
				return
			}
			logging.L().Warn(logPrefix+": "+message, zap.String("request_id", requestID), zap.Duration("interval", interval))
		},
	}
}

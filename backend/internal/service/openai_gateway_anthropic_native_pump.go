// 旧行泵入口仅转接唯一技术实现；超时和错误标识保持兼容。
package service

import (
	"bufio"
	"time"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
)

type anthropicNativeLinePump struct{ *forward.AnthropicLinePump }

func newAnthropicNativeLinePump(s *bufio.Scanner, d time.Duration) *anthropicNativeLinePump {
	return &anthropicNativeLinePump{forward.NewAnthropicLinePump(s, d)}
}
func (p *anthropicNativeLinePump) next() (string, error) { return p.Next() }
func (p *anthropicNativeLinePump) stop()                 { p.Stop() }

// anthropicNativeStreamInterval 返回本组转换路径适用的读间隔上限；
// gateway.stream_data_interval_timeout <= 0 时视为禁用。
func (s *OpenAIGatewayService) anthropicNativeStreamInterval() time.Duration {
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		return time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	return 0
}

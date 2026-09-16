//go:build unit

// 原行为测试继续经过相同转接；生产消费者清零后仅保留 unit 兼容。
package service

import (
	s09openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

func (s *OpenAIGatewayService) parseSSEUsageBytesWithType(data []byte, eventType string, usage *OpenAIUsage) bool {
	return s09openai.ParseSSEUsageBytesWithType(data, eventType, usage)
}

// 旧额度明细解析只委托 wire 唯一实现。
package service

import (
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

type openAIRateLimitResetCreditDetails = wire.OpenAIRateLimitResetCreditDetails

func parseOpenAIRateLimitResetCreditDetails(body []byte) (openAIRateLimitResetCreditDetails, error) {
	return wire.ParseOpenAIRateLimitResetCreditDetails(body)
}

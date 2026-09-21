package service

import (
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// resolveOpenAIForwardModel 解析 OpenAI 兼容转发使用的最终模型。
// messagesDispatchMappedModel 是渠道映射后再执行分组映射得到的账号层模型 D；
// 非空时账号映射必须以 D 为输入，普通 OpenAI 请求必须传空。
func resolveOpenAIForwardModel(account *Account, requestedModel, messagesDispatchMappedModel string) string {
	return accountModelPolicy(account).ForwardModel(requestedModel, messagesDispatchMappedModel)
}

func resolveOpenAICompactForwardModel(value *Account, model string) string {
	return acctcore.ResolveCompactForwardModel(AccountRecordView(value), model)
}

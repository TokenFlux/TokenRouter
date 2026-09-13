// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	strings "strings"
)

// ConfiguredAPIProtocol 只读取账号配置；请求级的协议解析结果由调用方持有。
func (a *Record) ConfiguredAPIProtocol() string {
	if a == nil || !a.IsCNProvider() {
		return APIProtocolChatCompletions
	}
	if _, unified := a.Credentials[UpstreamProtocolsKey]; unified {
		return APIProtocolAdaptive
	}
	switch strings.TrimSpace(a.GetCredential("api_protocol")) {
	case APIProtocolAdaptive:
		return APIProtocolAdaptive
	case APIProtocolAnthropic:
		return APIProtocolAnthropic
	case APIProtocolResponses:
		if a.SupportsNativeCNResponses() {
			return APIProtocolResponses
		}
	case APIProtocolChatCompletions:
		return APIProtocolChatCompletions
	}
	return APIProtocolChatCompletions
}

// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	strings "strings"
)

const OpenAIAuthModeAgentIdentity = "agentIdentity"

func (a *Record) IsOpenAIAgentIdentity() bool {
	if a == nil || !a.IsOpenAIOAuth() {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(a.GetCredential(OpenAIAuthModeCredentialKey)), OpenAIAuthModeAgentIdentity)
}

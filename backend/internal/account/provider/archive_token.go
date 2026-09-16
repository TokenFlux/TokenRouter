// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	openai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// DecodeArchiveIDToken 沿用原 OpenAI 导入的非认证解码，不新增过期或签名验证。
func DecodeArchiveIDToken(token string) (*account.ArchiveIdentityHints, error) {
	claims, err := openai.DecodeIDToken(token)
	if err != nil {
		return nil, err
	}
	info := claims.GetUserInfo()
	if info == nil {
		return nil, nil
	}
	return &account.ArchiveIdentityHints{Email: info.Email, PlanType: info.PlanType, ChatGPTAccountID: info.ChatGPTAccountID, ChatGPTUserID: info.ChatGPTUserID, OrganizationID: info.OrganizationID}, nil
}

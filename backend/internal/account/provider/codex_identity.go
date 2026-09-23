package provider

import (
	"net/http"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// CodexIdentityNamespace 在原 Header/metadata 投影时点读取当前凭据，不提前冻结命名空间。
func CodexIdentityNamespace(account *accountcore.Record) string {
	if account == nil || !account.IsOpenAIOAuthLike() {
		return ""
	}
	upstreamAccountID := strings.TrimSpace(account.GetChatGPTAccountID())
	if upstreamAccountID != "" {
		return openai.CodexAccountNamespace(openai.AccountIdentityInput{ChatGPTAccountID: upstreamAccountID, ChatGPTUserID: strings.TrimSpace(account.GetCredential("chatgpt_user_id"))})
	}
	if seed, ok := accountcore.CodexFingerprintSeed(account.Extra); ok {
		return openai.CodexAccountNamespace(openai.AccountIdentityInput{Seed: seed, HasSeed: true})
	}
	if account.Type == capability.AccountTypeSetupToken {
		return openai.CodexAccountNamespace(openai.AccountIdentityInput{SetupToken: strings.TrimSpace(account.GetOpenAIAccessToken())})
	}
	return ""
}

// CodexFingerprintIDsFromRequest 只解析本 attempt 的客户端会话，复用原指纹生成器。
func CodexFingerprintIDsFromRequest(value *accountcore.Record, headers http.Header) *openai.FingerprintIDs {
	if value == nil {
		return nil
	}
	mode := value.GetCodexFingerprintMode()
	if mode == accountcore.CodexFingerprintOff {
		return nil
	}
	session := ""
	if headers != nil {
		session = openai.ExtractClientSessionID(headers)
	}
	return CodexFingerprintIDs(value, session, mode)
}

// SetChatGPTAccountHeaders 保留 OAuth 资格和无 Header 的短路。
func SetChatGPTAccountHeaders(headers http.Header, value *accountcore.Record) {
	if headers == nil || value == nil || !value.IsOpenAIOAuthLike() {
		return
	}
	openai.SetChatGPTAccountHeaders(headers, value.GetChatGPTAccountID(), value.IsChatGPTAccountFedRAMP())
}

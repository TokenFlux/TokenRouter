package service

import (
	"context"
	"net/http"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"
)

const codexAccountIdentitySourceContextKey = "openai_codex_account_identity_source"

// prepareCodexAccountIdentitySource resolves credential shadows once per selected
// attempt. The handler reuses gin.Context across failover attempts, so every entry
// point overwrites the staged source before projecting outbound identity.
func (s *OpenAIGatewayService) prepareCodexAccountIdentitySource(ctx context.Context, c *gin.Context, account *Account) (*Account, error) {
	source := account
	if account != nil && account.IsShadow() {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return nil, err
		}
		source = resolved
	}
	if c != nil {
		c.Set(codexAccountIdentitySourceContextKey, source)
	}
	return source, nil
}

func codexAccountIdentitySource(c *gin.Context, fallback *Account) *Account {
	if c != nil {
		if staged, ok := c.Get(codexAccountIdentitySourceContextKey); ok {
			if source, ok := staged.(*Account); ok && source != nil {
				return source
			}
		}
	}
	return fallback
}

func codexAccountIdentityNamespace(account *Account) string {
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

func isolateOpenAIUpstreamSessionID(apiKeyID int64, account *Account, raw string) string {
	return openai.IsolateOpenAIUpstreamSessionID(apiKeyID, codexAccountIdentityNamespace(account), raw)
}

func scopeCodexAccountIdentityValue(account *Account, apiKeyID int64, kind, raw string) string {
	return openai.ScopeCodexAccountIdentityValue(codexAccountIdentityNamespace(account), apiKeyID, kind, raw)
}

func applyCodexAccountIdentityClientMetadataMap(requestBody map[string]any, account *Account, apiKeyID int64) bool {
	return openai.ApplyCodexAccountIdentityClientMetadataMap(requestBody, codexAccountIdentityNamespace(account), apiKeyID)
}

func applyCodexAccountIdentityClientMetadataRaw(body []byte, account *Account, apiKeyID int64) ([]byte, bool, error) {
	return openai.ApplyCodexAccountIdentityClientMetadataRaw(body, codexAccountIdentityNamespace(account), apiKeyID)
}

func applyCodexAccountIdentityHeaders(headers http.Header, account *Account, apiKeyID int64) {
	openai.ApplyCodexAccountIdentityHeaders(headers, codexAccountIdentityNamespace(account), apiKeyID)
}

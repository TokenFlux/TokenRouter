package service

import (
	"context"
	"strings"

	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

const openAICodexPATWhoamiURLDefault = "https://auth.openai.com/api/accounts/v1/user-auth-credential/whoami"

var openAICodexPATWhoamiURL = openAICodexPATWhoamiURLDefault

func (s *OpenAIOAuthService) ValidateCodexPersonalAccessToken(ctx context.Context, accessToken, proxyURL string) (*OpenAITokenInfo, error) {
	whoami, err := native.ValidatePersonalAccessToken(ctx, accessToken, proxyURL, openAICodexPATWhoamiURL)
	if err != nil {
		return nil, err
	}
	return accountcore.OpenAIPATTokenInfo(strings.TrimSpace(accessToken), whoami), nil
}

func NormalizeOpenAIPersonalAccessTokenCredentials(account *Account, tokenInfo *OpenAITokenInfo, credentials map[string]any) map[string]any {
	return accountcore.NormalizeOpenAIPersonalAccessTokenCredentials(AccountRecordView(account), tokenInfo, credentials)
}

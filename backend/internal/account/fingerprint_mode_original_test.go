package account_test

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/assert"
)

func newTestOAuthAccount(id int64, extra map[string]any) *accountcore.Record {
	if accountcore.CodexFingerprintModeRequiresSeed(accountcore.CodexFingerprintModeFromExtra(extra)) {
		if extra == nil {
			extra = make(map[string]any)
		}
		if _, exists := extra[accountcore.CodexFingerprintSeedExtraKey]; !exists {
			extra[accountcore.CodexFingerprintSeedExtraKey] = testCodexFingerprintSeed
		}
	}
	return &accountcore.Record{
		ID:       id,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Extra:    extra,
	}
}

func TestGetCodexFingerprintMode(t *testing.T) {
	tests := []struct {
		name     string
		account  *accountcore.Record
		expected accountcore.CodexFingerprintMode
	}{
		{"nil 账号", nil, accountcore.CodexFingerprintOff},
		{"非 OAuth 账号", &accountcore.Record{Platform: capability.PlatformOpenAI, Type: "api_key"}, accountcore.CodexFingerprintOff},
		{"OpenAI setup token", &accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeSetupToken, Extra: map[string]any{accountcore.CodexFingerprintModeExtraKey: "session"}}, accountcore.CodexFingerprintSession},
		{"Anthropic setup token", &accountcore.Record{Platform: capability.PlatformAnthropic, Type: capability.AccountTypeSetupToken, Extra: map[string]any{accountcore.CodexFingerprintModeExtraKey: "session"}}, accountcore.CodexFingerprintOff},
		// 收敛是显式 opt-in：缺省/空/非法一律 off（#5610）。存量账号普遍没有这个
		// extra 键，升级不得把它们静默切进收敛。
		{"无 extra 默认 off", newTestOAuthAccount(1, nil), accountcore.CodexFingerprintOff},
		{"空值默认 off", newTestOAuthAccount(1, map[string]any{accountcore.CodexFingerprintModeExtraKey: ""}), accountcore.CodexFingerprintOff},
		{"非法值默认 off", newTestOAuthAccount(1, map[string]any{accountcore.CodexFingerprintModeExtraKey: "invalid"}), accountcore.CodexFingerprintOff},
		{"显式 off", newTestOAuthAccount(1, map[string]any{accountcore.CodexFingerprintModeExtraKey: "off"}), accountcore.CodexFingerprintOff},
		{"device", newTestOAuthAccount(1, map[string]any{accountcore.CodexFingerprintModeExtraKey: "device"}), accountcore.CodexFingerprintDevice},
		{"session", newTestOAuthAccount(1, map[string]any{accountcore.CodexFingerprintModeExtraKey: "session"}), accountcore.CodexFingerprintSession},
		{"full", newTestOAuthAccount(1, map[string]any{accountcore.CodexFingerprintModeExtraKey: "full"}), accountcore.CodexFingerprintFull},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.account.GetCodexFingerprintMode())
		})
	}
}

const testCodexFingerprintSeed = "11111111-1111-4111-8111-111111111111"

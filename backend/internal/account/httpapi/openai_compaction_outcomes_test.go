//go:build unit

package httpapi

import (
	"fmt"
	"testing"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 手动测试保留认证错误和限流处理，但所有结果均不得改写管理员能力配置。
func TestManualCompactionTestsPreserveConfigurationAcrossOutcomes(t *testing.T) {
	for _, mode := range []string{accountcore.AccountTestModeCompact, accountcore.AccountTestModeLegacyCompact} {
		for _, status := range []int{200, 401, 404, 429} {
			t.Run(fmt.Sprintf("%s/%d", mode, status), func(t *testing.T) {
				extra := map[string]any{"openai_compact_mode": "force_off", accountcore.OpenAINativeCompactionV2ModeExtraKey: "force_on"}
				account := &accountcore.Record{ID: 13, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive,
					Credentials: map[string]any{"access_token": "test"}, Extra: extra}
				repo := &openAIProbeStore{}
				body := compactionTestV2SSESuccessBody
				if mode == accountcore.AccountTestModeLegacyCompact {
					body = `{"id":"legacy_test","status":"completed"}`
				}
				if status != 200 {
					body = fmt.Sprintf(`{"error":{"type":"usage_limit_reached","message":"test failure","resets_at":%d}}`, time.Now().Add(time.Hour).Unix())
				}
				resp := newJSONResponse(status, body)
				upstream := &openAIProbeTransport{resp: resp}
				svc := &accountprovider.OpenAIAccountTest{Store: repo, Transport: upstream}
				c, _ := newTestContext()
				err := executeOpenAIProbe(t, svc, c, account, "gpt-5.4", "", mode)
				if status == 200 {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
				require.Equal(t, "force_off", account.Extra["openai_compact_mode"])
				require.Equal(t, "force_on", account.Extra[accountcore.OpenAINativeCompactionV2ModeExtraKey])
				for _, key := range accountcore.DeprecatedOpenAIAccountExtraKeys {
					require.NotContains(t, repo.updatedExtra, key)
				}
				require.NotContains(t, repo.updatedExtra, "openai_compact_mode")
				require.NotContains(t, repo.updatedExtra, accountcore.OpenAINativeCompactionV2ModeExtraKey)
				if status == 401 {
					require.Equal(t, account.ID, repo.setErrorID)
				}
				if status == 429 {
					require.Equal(t, account.ID, repo.rateLimitedID)
				}
			})
		}
	}
}

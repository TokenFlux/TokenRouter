//go:build unit

package provider

import (
	"context"
	"errors"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

func TestAdminService_EnsureOpenAIPrivacy_RetriesNonSuccessModes(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{openai.PrivacyModeFailed, openai.PrivacyModeCFBlocked} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			privacyCalls := 0
			factory := func(proxyURL string) (*req.Client, error) {
				privacyCalls++
				return nil, errors.New("factory failed")
			}

			account := &accountcore.Record{
				ID:       101,
				Platform: capability.PlatformOpenAI,
				Type:     capability.AccountTypeOAuth,
				Credentials: map[string]any{
					"access_token": "token-1",
				},
				Extra: map[string]any{
					"privacy_mode": mode,
				},
			}

			svc := accountcore.NewPrivacyService(&privacyIdentityWriter{current: *account}, nil, PrivacyOptions(factory, openai.PrivacyEndpoints{}))
			got := svc.EnsureOpenAIPrivacy(context.Background(), account)

			require.Equal(t, openai.PrivacyModeFailed, got)
			require.Equal(t, 1, privacyCalls)
		})
	}
}

func TestTokenRefreshService_ensureOpenAIPrivacy_RetriesNonSuccessModes(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{openai.PrivacyModeFailed, openai.PrivacyModeCFBlocked} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			privacyCalls := 0
			factory := func(proxyURL string) (*req.Client, error) {
				privacyCalls++
				return nil, errors.New("factory failed")
			}

			account := &accountcore.Record{
				ID:       202,
				Platform: capability.PlatformOpenAI,
				Type:     capability.AccountTypeOAuth,
				Credentials: map[string]any{
					"access_token": "token-2",
				},
				Extra: map[string]any{
					"privacy_mode": mode,
				},
			}

			svc := accountcore.NewPrivacyService(&privacyIdentityWriter{current: *account}, nil, PrivacyOptions(factory, openai.PrivacyEndpoints{}))
			svc.RefreshOpenAIPrivacy(context.Background(), account)

			require.Equal(t, 1, privacyCalls)
		})
	}
}

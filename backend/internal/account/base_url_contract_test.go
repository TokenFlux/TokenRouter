//go:build unit

package account_test

import (
	"time"

	"github.com/google/uuid"

	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/stretchr/testify/require"
)

func TestGetBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		account  accountcore.Record
		expected string
	}{
		{
			name: "non-apikey type returns empty",
			account: accountcore.Record{
				Type:     capability.AccountTypeOAuth,
				Platform: capability.PlatformAnthropic,
			},
			expected: "",
		},
		{
			name: "apikey without base_url returns default anthropic",
			account: accountcore.Record{
				Type:        capability.AccountTypeAPIKey,
				Platform:    capability.PlatformAnthropic,
				Credentials: map[string]any{},
			},
			expected: "https://api.anthropic.com",
		},
		{
			name: "apikey with custom base_url",
			account: accountcore.Record{
				Type:        capability.AccountTypeAPIKey,
				Platform:    capability.PlatformAnthropic,
				Credentials: map[string]any{"base_url": "https://custom.example.com"},
			},
			expected: "https://custom.example.com",
		},
		{
			name: "antigravity apikey auto-appends /antigravity",
			account: accountcore.Record{
				Type:        capability.AccountTypeAPIKey,
				Platform:    capability.PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com"},
			},
			expected: "https://upstream.example.com/antigravity",
		},
		{
			name: "antigravity apikey trims trailing slash before appending",
			account: accountcore.Record{
				Type:        capability.AccountTypeAPIKey,
				Platform:    capability.PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com/"},
			},
			expected: "https://upstream.example.com/antigravity",
		},
		{
			name: "antigravity non-apikey returns empty",
			account: accountcore.Record{
				Type:        capability.AccountTypeOAuth,
				Platform:    capability.PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com"},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.account.GetBaseURL()
			if result != tt.expected {
				t.Errorf("GetBaseURL() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestGetGeminiBaseURL(t *testing.T) {
	const defaultGeminiURL = "https://generativelanguage.googleapis.com"

	tests := []struct {
		name     string
		account  accountcore.Record
		expected string
	}{
		{
			name: "apikey without base_url returns default",
			account: accountcore.Record{
				Type:        capability.AccountTypeAPIKey,
				Platform:    capability.PlatformGemini,
				Credentials: map[string]any{},
			},
			expected: defaultGeminiURL,
		},
		{
			name: "apikey with custom base_url",
			account: accountcore.Record{
				Type:        capability.AccountTypeAPIKey,
				Platform:    capability.PlatformGemini,
				Credentials: map[string]any{"base_url": "https://custom-gemini.example.com"},
			},
			expected: "https://custom-gemini.example.com",
		},
		{
			name: "antigravity apikey auto-appends /antigravity",
			account: accountcore.Record{
				Type:        capability.AccountTypeAPIKey,
				Platform:    capability.PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com"},
			},
			expected: "https://upstream.example.com/antigravity",
		},
		{
			name: "antigravity apikey trims trailing slash",
			account: accountcore.Record{
				Type:        capability.AccountTypeAPIKey,
				Platform:    capability.PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com/"},
			},
			expected: "https://upstream.example.com/antigravity",
		},
		{
			name: "antigravity oauth does NOT append /antigravity",
			account: accountcore.Record{
				Type:        capability.AccountTypeOAuth,
				Platform:    capability.PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com"},
			},
			expected: "https://upstream.example.com",
		},
		{
			name: "oauth without base_url returns default",
			account: accountcore.Record{
				Type:        capability.AccountTypeOAuth,
				Platform:    capability.PlatformAntigravity,
				Credentials: map[string]any{},
			},
			expected: defaultGeminiURL,
		},
		{
			name: "nil credentials returns default",
			account: accountcore.Record{
				Type:     capability.AccountTypeAPIKey,
				Platform: capability.PlatformGemini,
			},
			expected: defaultGeminiURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.account.GetGeminiBaseURL(defaultGeminiURL)
			if result != tt.expected {
				t.Errorf("GetGeminiBaseURL() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestHasGeminiThirdPartyBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		account  accountcore.Record
		expected bool
	}{
		{
			name: "custom Gemini-compatible endpoint",
			account: accountcore.Record{
				Platform: capability.PlatformGemini,
				Type:     capability.AccountTypeAPIKey,
				Credentials: map[string]any{
					accountcore.GeminiProviderTypeCredentialKey: accountcore.GeminiProviderTypeThirdParty,
					"base_url": "https://provider.example.test/v1beta",
				},
			},
			expected: true,
		},
		{
			name: "missing base URL",
			account: accountcore.Record{
				Platform: capability.PlatformGemini,
				Type:     capability.AccountTypeAPIKey,
				Credentials: map[string]any{
					accountcore.GeminiProviderTypeCredentialKey: accountcore.GeminiProviderTypeThirdParty,
				},
			},
			expected: false,
		},
		{
			name: "official Gemini endpoint",
			account: accountcore.Record{
				Platform: capability.PlatformGemini,
				Type:     capability.AccountTypeAPIKey,
				Credentials: map[string]any{
					accountcore.GeminiProviderTypeCredentialKey: accountcore.GeminiProviderTypeThirdParty,
					"base_url": "https://generativelanguage.googleapis.com/v1beta",
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.account.HasGeminiThirdPartyBaseURL())
		})
	}
}

func TestBuildAccountForCreateRequiresCustomGeminiThirdPartyBaseURL(t *testing.T) {
	invalidInput := &accountcore.CreateAccountInput{
		Name:     "third-party Gemini",
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			accountcore.GeminiProviderTypeCredentialKey: accountcore.GeminiProviderTypeThirdParty,
			"base_url": "https://generativelanguage.googleapis.com",
		},
	}

	_, err := accountcore.BuildAccountForCreate(invalidInput, nil, accountcore.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: uuid.NewString})
	require.Error(t, err)
	require.Equal(t, "GEMINI_THIRD_PARTY_BASE_URL_REQUIRED", apperror.Reason(err))

	validInput := &accountcore.CreateAccountInput{
		Name:     "third-party Gemini",
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			accountcore.GeminiProviderTypeCredentialKey: accountcore.GeminiProviderTypeThirdParty,
			"base_url": "https://provider.example.test",
		},
	}
	account, err := accountcore.BuildAccountForCreate(validInput, nil, accountcore.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: uuid.NewString})
	require.NoError(t, err)
	require.NotNil(t, account)
}

package provider

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestFilterOpenAIResponsesNoneReasoningEffortForAccount(t *testing.T) {
	tests := []struct {
		name          string
		account       *accountcore.Record
		body          string
		wantNested    bool
		wantFlat      bool
		wantSummary   bool
		wantReasoning bool
	}{
		{
			name:          "Kimi removes none placeholder",
			account:       &accountcore.Record{Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey},
			body:          `{"reasoning":{"effort":"none"},"reasoning_effort":"NONE"}`,
			wantReasoning: false,
		},
		{
			name:          "custom compatible endpoint removes none placeholder",
			account:       &accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://compat.example/v1"}},
			body:          `{"reasoning":{"effort":"none"},"reasoning_effort":"NONE"}`,
			wantReasoning: false,
		},
		{
			name:          "preserves other reasoning members",
			account:       &accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey},
			body:          `{"reasoning":{"effort":" none ","summary":"auto"}}`,
			wantSummary:   true,
			wantReasoning: true,
		},
		{
			name:          "official OpenAI API preserves none",
			account:       &accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey},
			body:          `{"reasoning":{"effort":"none"},"reasoning_effort":"none"}`,
			wantNested:    true,
			wantFlat:      true,
			wantReasoning: true,
		},
		{
			name:          "OpenAI OAuth preserves none",
			account:       &accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth},
			body:          `{"reasoning":{"effort":"none"}}`,
			wantNested:    true,
			wantReasoning: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FilterOpenAIResponsesNoneReasoningEffortForAccount(tt.account, []byte(tt.body))
			require.NoError(t, err)
			require.Equal(t, tt.wantNested, gjson.GetBytes(got, "reasoning.effort").Exists())
			require.Equal(t, tt.wantFlat, gjson.GetBytes(got, "reasoning_effort").Exists())
			require.Equal(t, tt.wantSummary, gjson.GetBytes(got, "reasoning.summary").Exists())
			require.Equal(t, tt.wantReasoning, gjson.GetBytes(got, "reasoning").Exists())
		})
	}
}

func TestFilterOpenAIResponsesNoneReasoningEffortForAccount_APIKeyAutomaticPassthroughPreservesRequest(t *testing.T) {
	body := []byte(`{"model":"qwen3.8-27b","input":"hi","max_output_tokens":20,"reasoning":{"effort":"none"},"presence_penalty":1.5}`)
	account := &accountcore.Record{
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://compat.example/v1",
		},
		Extra: map[string]any{"openai_passthrough": true},
	}

	got, err := FilterOpenAIResponsesNoneReasoningEffortForAccount(account, body)

	require.NoError(t, err)
	require.JSONEq(t, string(body), string(got))
}

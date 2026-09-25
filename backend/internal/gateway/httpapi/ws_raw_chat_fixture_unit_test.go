//go:build unit

package httpapi

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

func rawChatCompletionsTestConfig() *wsFixtureOptions {
	return &wsFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{Enabled: false, AllowInsecureHTTP: true}}}
}
func rawChatCompletionsTestAccount() *provider.ExecutionAccount {
	return &provider.ExecutionAccount{Record: account.Record{LoadLocation: time.LoadLocation, ID: 101, Name: "raw-openai-apikey", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test", "base_url": "http://upstream.example"}}}
}
